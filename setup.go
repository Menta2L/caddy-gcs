package gcsproxy

import (
	"github.com/mholt/caddy"
	"github.com/mholt/caddy/caddyhttp/httpserver"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"os"
	"sync"
)

func init() {
	httpserver.RegisterDevDirective("gcs", "prometheus")
	caddy.RegisterPlugin("gcs", caddy.Plugin{
		ServerType: "http",
		Action:     setup,
	})
}

var once sync.Once

func setup(c *caddy.Controller) error {
	config, err := parseConfig(c)

	if err != nil {
		return err
	}
	if _, err := os.Stat(config.Path); os.IsNotExist(err) {
		err := os.Mkdir(config.Path, os.ModePerm)
		if err != nil {
			return err
		}
	}
	config.metrics.handler = promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
	})

	once.Do(func() {
		c.OnStartup(config.metrics.start)
	})
	cfg := httpserver.GetConfig(c)
	if config.metrics.useCaddyAddr {
		cfg.AddMiddleware(func(next httpserver.Handler) httpserver.Handler {
			return httpserver.HandlerFunc(func(w http.ResponseWriter, r *http.Request) (int, error) {
				if r.URL.Path == config.metrics.path {
					config.metrics.handler.ServeHTTP(w, r)
					return 0, nil
				}
				return next.ServeHTTP(w, r)
			})
		})
	}
	httpserver.GetConfig(c).AddMiddleware(func(next httpserver.Handler) httpserver.Handler {
		return NewHandler(next, config)
	})

	return nil
}
