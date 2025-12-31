set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64
go build -trimpath -ldflags="-s -w" -o frps_%GOOS%_%GOARCH% ./cmd/frps
upx -9 frps_%GOOS%_%GOARCH%

go build -trimpath -ldflags="-s -w" -o frpc_%GOOS%_%GOARCH% ./cmd/frpc
upx -9 frpc_%GOOS%_%GOARCH%
pause
