package main

type mailRule struct {
	Queue string `mapstructure:"queue"`
	Action string `mapstructure:"action"`
}

var defaultRule = mailRule{
	Queue: "General",
	Action: "correspond",
}

type configRule struct {
	Address string `mapstructure:"address"`
	mailRule `mapstructure:",squash"`
}

type ruleSet = map[string]mailRule

func toRuleSet(rulelist []configRule) ruleSet {
	rez := make(map[string]mailRule)
	for i := len(rulelist)-1; i >= 0; i-- {
		addr := rulelist[i].Address
		rez[addr] = rulelist[i].mailRule
	}
	return rez
}


type config struct {
	Port int `mapstructure:"port"`
	Address string `mapstructure:"address"`
	RTUrl string `mapstructure:"rt_url"`
	Key string `mapstructure:"key"`
	Default mailRule `mapstructure:"default"`
	Rules []configRule `mapstructure:"rules"`
	Verbose int `mapstructure:"verbose"`
}
