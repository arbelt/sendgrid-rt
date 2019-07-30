
.PHONY: build

build:
	goreleaser release --skip-publish --snapshot --rm-dist


upx: ./dist/sendgrid-rt_darwin_amd64/sendgrid-rt
# upx: dist/darwin_386/sendgrid-rt
upx: ./dist/sendgrid-rt_linux_amd64/sendgrid-rt
# upx: dist/linux_386/sendgrid-rt
upx:
	upx $?
