package main

import (
	"github.com/mholt/caddy/caddy/caddymain"

	_ "github.com/Menta2L/caddy-gcsproxy"
)

func main() {
	caddymain.EnableTelemetry = false
	caddymain.Run()
}
