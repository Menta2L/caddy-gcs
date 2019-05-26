package gcsproxy

import (
	"encoding/json"
	"fmt"
	"github.com/mholt/caddy"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"time"
)

var (
	defaultStatusHeader = "X-Cache-Status"
	defaultLockTimeout  = time.Duration(5) * time.Minute
	defaultMaxAge       = time.Duration(5) * time.Minute
	defaultPath         = ""
)

// defaultCacheKeyTemplate is the placeholder template that will be used to
// generate the cache key.
const defaultCacheKeyTemplate = "{method} {host}{path}?{query}"

// Config specifies configuration parsed for Caddyfile
type Config struct {
	StatusHeader     string
	DefaultMaxAge    time.Duration
	LockTimeout      time.Duration
	CacheRules       []CacheRule
	Path             string
	CacheKeyTemplate string
	Buckets          []Bucket
	uiPath           string
	host             string
	metrics          *Metrics
}

// Config specifies configuration parsed for Caddyfile
type Bucket struct {
	Name        string
	Credentials *googleCloudCredential
}
type googleCloudCredential struct {
	GoogleAccessID string `json:"client_email"`
	PrivateKey     string `json:"private_key"`
}

func emptyConfig() *Config {
	return &Config{
		StatusHeader:     defaultStatusHeader,
		DefaultMaxAge:    defaultMaxAge,
		LockTimeout:      defaultLockTimeout,
		CacheRules:       []CacheRule{},
		Path:             defaultPath,
		CacheKeyTemplate: defaultCacheKeyTemplate,
	}
}
func parseConfig(c *caddy.Controller) (*Config, error) {
	var buckets []Bucket
	var (
		metrics *Metrics
	)
	config := emptyConfig()
	c.Next() // Skip "cache" literal
	if len(c.RemainingArgs()) > 1 {
		return config, c.Err("Unexpected value " + c.Val())
	}
	host, _, _ := net.SplitHostPort(c.Key)
	config.host = host
	for c.NextBlock() {
		parameter := c.Val()
		args := c.RemainingArgs()

		switch parameter {
		case "status_header":
			if len(args) != 1 {
				return nil, c.Err("Invalid usage of status_header in cache config.")
			}
			config.StatusHeader = args[0]
		case "lock_timeout":
			if len(args) != 1 {
				return nil, c.Err("Invalid usage of lock_timeout in cache config.")
			}
			duration, err := time.ParseDuration(c.Val())
			if err != nil {
				return nil, c.Err("lock_timeout: Invalid duration " + c.Val())
			}
			config.LockTimeout = duration
		case "default_max_age":
			if len(args) != 1 {
				return nil, c.Err("Invalid usage of default_max_age in cache config.")
			}
			duration, err := time.ParseDuration(c.Val())
			if err != nil {
				return nil, c.Err("default_max_age: Invalid duration " + c.Val())
			}
			config.DefaultMaxAge = duration
		case "path":
			if len(args) != 1 {
				return nil, c.Err("Invalid usage of path in cache config.")
			}
			config.Path = args[0]
		case "match_header":
			if len(args) < 2 {
				return nil, c.Err("Invalid usage of match_header in cache config.")
			}
			cacheRule := &HeaderCacheRule{Header: args[0], Value: args[1:]}
			config.CacheRules = append(config.CacheRules, cacheRule)
		case "match_path":
			if len(args) != 1 {
				return nil, c.Err("Invalid usage of match_path in cache config.")
			}
			cacheRule := &PathCacheRule{Path: args[0]}
			config.CacheRules = append(config.CacheRules, cacheRule)
		case "cache_key":
			if len(args) != 1 {
				return nil, c.Err("Invalid usage of cache_key in cache config.")
			}
			config.CacheKeyTemplate = args[0]
		case "bucket":
			var bucket Bucket
			if len(args) != 2 {
				return nil, c.Err("Invalid usage of cache_key in cache config.")
			}
			file := args[1]
			if _, err := os.Stat(file); os.IsNotExist(err) {
				return nil, fmt.Errorf("credential file for '%s' %s not exist", args[0], file)
			}
			data, err := ioutil.ReadFile(file)
			if err != nil {
				return nil, fmt.Errorf("error reading service account key file: %v", err)
			}
			var gcloudCredential = &googleCloudCredential{}
			err = json.Unmarshal(data, gcloudCredential)
			if err != nil {
				return nil, fmt.Errorf("error parsing service account credentials: %v", err)
			}
			bucket.Name = args[0]
			bucket.Credentials = gcloudCredential
			buckets = append(buckets, bucket)
		case "stats":
			c.Next() // Skip "stats" literal
			for c.NextBlock() {
				parameter := c.Val()
				switch parameter {
				case "prometheus":
					c.Next() // Skip "prometheus" literal
					metrics = NewMetrics()
					args := c.RemainingArgs()

					switch len(args) {
					case 0:
					case 1:
						metrics.addr = args[0]
					default:
						return nil, c.ArgErr()
					}
					addrSet := false
					for c.NextBlock() {
						switch c.Val() {
						case "path":
							args = c.RemainingArgs()
							if len(args) != 1 {
								return nil, c.ArgErr()
							}
							metrics.path = args[0]
						case "address":
							if metrics.useCaddyAddr {
								return nil, c.Err("prometheus: address and use_caddy_addr options may not be used together")
							}
							args = c.RemainingArgs()
							if len(args) != 1 {
								return nil, c.ArgErr()
							}
							metrics.addr = args[0]
							addrSet = true
						case "hostname":
							args = c.RemainingArgs()
							if len(args) != 1 {
								return nil, c.ArgErr()
							}
							metrics.hostname = args[0]
						case "use_caddy_addr":
							if addrSet {
								return nil, c.Err("prometheus: address and use_caddy_addr options may not be used together")
							}
							metrics.useCaddyAddr = true
						case "label":
							args = c.RemainingArgs()
							if len(args) != 2 {
								return nil, c.ArgErr()
							}

							labelName := strings.TrimSpace(args[0])
							labelValuePlaceholder := args[1]

							metrics.extraLabels = append(metrics.extraLabels, extraLabel{name: labelName, value: labelValuePlaceholder})
						default:
							return nil, c.Errf("prometheus: unknown item: %s", c.Val())
						}
					}
					config.metrics = metrics
				default:
					return nil, c.Err("Unknown cache parameter: " + parameter)
				}

			}

		default:
			return nil, c.Err("Unknown cache parameter: " + parameter)
		}
	}
	config.Buckets = buckets
	return config, nil
}
