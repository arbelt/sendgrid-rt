package main

import (
	"encoding/json"
	"fmt"
	"github.com/apex/log"
	"github.com/apex/log/handlers/text"
	"github.com/davecgh/go-spew/spew"
	"github.com/gorilla/mux"
	"github.com/gorilla/schema"
	"github.com/gorilla/handlers"
	"github.com/pkg/errors"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"io/ioutil"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type rtMailData struct {
	mailRule
	Message string
}

var (
	conf       config
	hclient    *http.Client
	rtEndpoint string
	rules      ruleSet
)

func init() {
	log.SetHandler(text.New(os.Stdout))
	log.SetLevel(log.InfoLevel)

	pflag.CountP("verbose", "v", "Verbosity")
	pflag.Parse()

	_ = viper.BindPFlag("verbose", pflag.Lookup("verbose"))

	viper.SetConfigName("sendgrid-rt")
	viper.AddConfigPath("/")
	viper.AddConfigPath("/etc")
	viper.AddConfigPath(".")
	viper.SetEnvPrefix("sgrt")
	viper.AutomaticEnv()
	viper.SetDefault("Port", 9090)
	viper.SetDefault("Address", "localhost")
	viper.SetDefault("RT_Url", "http://127.0.0.1")
	viper.SetDefault("key", "")
	viper.SetDefault("default", defaultRule)
	err := viper.ReadInConfig()
	if err != nil {
		log.WithError(err).Warn("Error reading config")
	}
	err = viper.Unmarshal(&conf)
	if err != nil {
		log.WithError(err).Warn("Error loading config")
	}

	switch conf.Verbose {
	case 0:
		log.SetLevel(log.InfoLevel)
	default:
		log.SetLevel(log.DebugLevel)
	}

	rules = toRuleSet(conf.Rules)

	log.Debug(spew.Sprintf("%+v", rules))

	log.Debug(spew.Sprintf("%+v", conf))

	rtEndpoint = fmt.Sprintf("%s/%s",
		strings.TrimSuffix(conf.RTUrl, "/"),
		"REST/1.0/NoAuth/mail-gateway")

	hclient = &http.Client{
		Timeout: time.Second * 20,
		Transport: &http.Transport{
			TLSHandshakeTimeout: time.Second * 5,
		},
	}
}

func main() {
	router := mux.NewRouter()

	mw := KeyMiddleware{Key: conf.Key}

	router.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprintln(writer, "Hello!")
	}).Queries("key", "{key}")

	router.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprintln(writer, "Hello!")
	})

	router.
		Path("/parse/").
		Methods("POST").
		HeadersRegexp("Content-Type", "multipart/form-data").
		HandlerFunc(sgRawHandler)

	router.Use(mw.MiddleWare)
	router.Use(handlers.RecoveryHandler())

	log.WithField("address", conf.Address).
		WithField("port", conf.Port).
		Info("server start")
	log.WithField("address", rtEndpoint).Info("RT Endpoint")

	server := http.Server{
		Addr:    fmt.Sprintf("%s:%d", conf.Address, conf.Port),
		Handler: router,
	}

	err := server.ListenAndServe()
	if err != nil {
		log.Fatal(err.Error())
	}
}

type envelope struct {
	To   []string `json:"to"`
	From string   `json:"from"`
}

type Envelope struct {
	*envelope
}

type SGInboundRaw struct {
	Email    string   `schema:"email"`
	To       string   `schema:"to"`
	Envelope Envelope `schema:"envelope"`
	Cc       string   `schema:"cc"`
	From     string   `schema:"from"`
	Subject  string   `schema:"subject"`
}

func (e *Envelope) UnmarshalText(text []byte) (err error) {
	var env envelope
	err = json.Unmarshal(text, &env)
	e.envelope = &env
	return
}

type KeyMiddleware struct {
	Key string
}

func (m *KeyMiddleware) MiddleWare(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.FormValue("key")

		if key == m.Key {
			next.ServeHTTP(w, r)
		} else {
			http.Error(w, "Forbidden", http.StatusForbidden)
		}
	})
}

func sgRawHandler(w http.ResponseWriter, r *http.Request) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	log.Infof("%v %v", mediaType, params)
	if strings.HasPrefix(mediaType, "multipart/") {
		err = r.ParseMultipartForm(64 << 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		err = r.ParseForm()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	decoder := schema.NewDecoder()
	decoder.IgnoreUnknownKeys(true)
	inbound := new(SGInboundRaw)

	err = decoder.Decode(inbound, r.PostForm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}

	log.WithField("From", inbound.From).
		WithField("To", inbound.To).
		WithField("Cc", inbound.Cc).
		WithField("Subject", inbound.Subject).
		WithField("Envelope", spew.Sprintf("%+v", inbound.Envelope)).
		Info("received email")

	rtData := getRoute(rules, inbound)

	log.WithField("queue", rtData.Queue).
		WithField("action", rtData.Action).
		Debug("Posting to RT")

	err = postMailData(rtData)
	if err != nil {
		log.WithError(err).Error("failed to post data")
		return
	}
}

func getRoute(rules ruleSet, eml *SGInboundRaw) rtMailData {
	for _, a := range eml.Envelope.To {
		r, ok := rules[a]
		if ok {
			log.WithField("address", a).
				WithField("action", r.Action).
				WithField("queue", r.Queue).
				Info("calculated route")
			return rtMailData{
				Message:  eml.Email,
				mailRule: r,
			}
		}
	}

	return rtMailData{
		mailRule: conf.Default,
		Message:  eml.Email,
	}
}

func postMailData(data rtMailData) (err error) {
	form := url.Values{
		"action":  []string{data.Action},
		"queue":   []string{data.Queue},
		"message": []string{data.Message},
	}

	resp, e := hclient.PostForm(rtEndpoint, form)
	if e != nil {
		err = errors.Wrap(e, "post failed")
		return
	}
	if resp != nil {
		defer resp.Body.Close()
	}
	bodyBytes, _ := ioutil.ReadAll(resp.Body)
	body := string(bodyBytes)
	log.WithField("status", resp.StatusCode).
		WithField("body", body).
		Info("RT response")
	return
}
