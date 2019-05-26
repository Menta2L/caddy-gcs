package gcsproxy

import (
	"cloud.google.com/go/storage"
	"fmt"
	"github.com/mholt/caddy"
	"github.com/mholt/caddy/caddyhttp/httpserver"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Handler is the main cache middleware
type Handler struct {
	// Handler configuration
	Config *Config

	// Cache is where entries are stored
	Cache *HTTPCache

	// Next handler
	Next httpserver.Handler

	// Handles locking for different URLs
	URLLocks *URLLock
	Stats    *Stats
}

const (
	cacheHit    = "hit"
	cacheMiss   = "miss"
	cacheSkip   = "skip"
	cacheBypass = "bypass"
)

var (
	contextKeysToPreserve = []caddy.CtxKey{
		httpserver.OriginalURLCtxKey,
		httpserver.ReplacerCtxKey,
		httpserver.RemoteUserCtxKey,
		httpserver.MitmCtxKey,
		httpserver.RequestIDCtxKey,
		"path_prefix",
		"mitm",
	}
	defaultExpire = 300
)

func getKey(cacheKeyTemplate string, r *http.Request) string {
	return httpserver.NewReplacer(r, nil, "").Replace(cacheKeyTemplate)
}

// NewHandler creates a new Handler using Next middleware
func NewHandler(Next httpserver.Handler, config *Config) *Handler {
	return &Handler{
		Config:   config,
		Cache:    NewHTTPCache(config.CacheKeyTemplate),
		URLLocks: NewURLLock(),
		Next:     Next,
		Stats:    &Stats{},
	}
}

/* Responses */

func copyHeaders(from http.Header, to http.Header) {
	for k, values := range from {
		for _, v := range values {
			to.Add(k, v)
		}
	}
}

func (handler *Handler) addStatusHeaderIfConfigured(w http.ResponseWriter, status string) {
	handler.Stats.Inc(status)
	if rec, ok := w.(*httpserver.ResponseRecorder); ok {
		rec.Replacer.Set("cache_status", status)
	}

	if handler.Config.StatusHeader != "" {
		w.Header().Add(handler.Config.StatusHeader, status)
	}
}

func (handler *Handler) respond(w http.ResponseWriter, entry *HTTPCacheEntry, cacheStatus string) (int, error) {
	handler.addStatusHeaderIfConfigured(w, cacheStatus)

	copyHeaders(entry.Response.snapHeader, w.Header())
	w.WriteHeader(entry.Response.Code)

	err := entry.WriteBodyTo(w)

	return entry.Response.Code, err
}

/* Handler */

func shouldUseCache(req *http.Request) bool {
	// TODO Add more logic like get params, ?nocache=true

	if req.Method != "GET" && req.Method != "HEAD" {
		// Only cache Get and head request
		return false
	}

	// Range requests still not supported
	// It may happen that the previous request for this url has a successful response
	// but for another Range. So a special handling is needed
	if req.Header.Get("range") != "" {
		return false
	}

	if strings.ToLower(req.Header.Get("Connection")) == "upgrade" && strings.ToLower(req.Header.Get("Upgrade")) == "websocket" {
		return false
	}

	return true
}

func popOrNil(errChan chan error) (err error) {
	select {
	case err = <-errChan:
	default:
	}
	return
}

func (handler *Handler) fetchUpstream(req *http.Request) (*HTTPCacheEntry, error) {
	// Create a new empty response
	response := NewResponse()
	errChan := make(chan error, 1)
	var found = false
	var res *http.Response
	go func(req *http.Request, response *Response) {
		for _, bucket := range handler.Config.Buckets {
			expires := time.Now().Add(time.Duration(defaultExpire) * time.Second)
			signedURLOptions := storage.SignedURLOptions{
				GoogleAccessID: bucket.Credentials.GoogleAccessID,
				PrivateKey:     []byte(bucket.Credentials.PrivateKey),
				Method:         "GET",
				Expires:        expires,
			}
			object := strings.TrimLeft(req.URL.Path, "/")
			url, err := storage.SignedURL(bucket.Name, object, &signedURLOptions)
			if err != nil {
				fmt.Println("error %s \n", err)
				continue
			}
			res, err = http.Get(url)
			if err != nil {
				continue
			}
			if res.StatusCode != 200 {
				continue
			} else {
				found = true
				break
			}
		}
		if found {
			response.WriteHeader(res.StatusCode)
			response.WaitBody()
			body, _ := ioutil.ReadAll(res.Body)
			response.Write(body)
			response.Close()
		} else {
			response.WriteHeader(404)
		}
		errChan <- fmt.Errorf("not found")
	}(req, response)
	// Wait headers to be sent
	response.WaitHeaders()
	// Create a new CacheEntry
	return NewHTTPCacheEntry(getKey(handler.Config.CacheKeyTemplate, req), req, response, handler.Config), popOrNil(errChan)
}

func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) (int, error) {

	hostname := handler.Config.metrics.hostname

	if hostname == "" {
		originalHostname, err := host(r)
		if err != nil {
			hostname = "-"
		} else {
			hostname = originalHostname
		}
	}
	fam := "1"
	if isIPv6(r.RemoteAddr) {
		fam = "2"
	}

	proto := strconv.Itoa(r.ProtoMajor)
	proto = proto + "." + strconv.Itoa(r.ProtoMinor)
	var extraLabelValues []string
	requestCount.WithLabelValues(append([]string{hostname, fam, proto}, extraLabelValues...)...).Inc()
	//replacer := httpserver.NewReplacer(r, rw, "")
	//for _, label := range handler.Config.metrics.extraLabels {
	//	extraLabelValues = append(extraLabelValues, replacer.Replace(label.value))
	//}

	//start := time.Now()
	if !shouldUseCache(r) {
		handler.addStatusHeaderIfConfigured(w, cacheBypass)
		responseStatus.WithLabelValues(append([]string{hostname, fam, proto, cacheBypass}, extraLabelValues...)...).Inc()
		return handler.Next.ServeHTTP(w, r)
	}

	lock := handler.URLLocks.Adquire(getKey(handler.Config.CacheKeyTemplate, r))

	// Lookup correct entry
	previousEntry, exists := handler.Cache.Get(r)

	// First case: CACHE HIT
	// The response exists in cache and is public
	// It should be served as saved
	if exists && previousEntry.isPublic {
		lock.Unlock()
		responseStatus.WithLabelValues(append([]string{hostname, fam, proto, cacheHit}, extraLabelValues...)...).Inc()
		return handler.respond(w, previousEntry, cacheHit)
	}

	// Second case: CACHE SKIP
	// The response is in cache but it is not public
	// It should NOT be served from cache
	// It should be fetched from upstream and check the new headers
	// To check if the new response changes to public
	if exists && !previousEntry.isPublic {
		lock.Unlock()
		start := time.Now()
		entry, err := handler.fetchUpstream(r)
		gcsRequestDuration.WithLabelValues(append([]string{hostname, fam, proto}, extraLabelValues...)...).Observe(time.Since(start).Seconds())
		if err != nil {
			responseStatus.WithLabelValues(append([]string{hostname, fam, proto, "error"}, extraLabelValues...)...).Inc()
			return entry.Response.Code, err
		}

		// Case when response was private but now is public
		if entry.isPublic {
			err := entry.setStorage(handler.Config)
			if err != nil {
				responseStatus.WithLabelValues(append([]string{hostname, fam, proto, "error"}, extraLabelValues...)...).Inc()
				fmt.Println("error %s \n", err)
				return 500, err
			}
			responseStatus.WithLabelValues(append([]string{hostname, fam, proto, cacheMiss}, extraLabelValues...)...).Inc()
			handler.Cache.Put(r, entry)
			return handler.respond(w, entry, cacheMiss)
		}

		return handler.respond(w, entry, cacheSkip)
	}

	// Third case: CACHE MISS
	// The response is not in cache
	// It should be fetched from upstream and save it in cache
	start := time.Now()
	entry, err := handler.fetchUpstream(r)
	gcsRequestDuration.WithLabelValues(append([]string{hostname, fam, proto}, extraLabelValues...)...).Observe(time.Since(start).Seconds())
	if err != nil {
		lock.Unlock()
		responseStatus.WithLabelValues(append([]string{hostname, fam, proto, "error"}, extraLabelValues...)...).Inc()
		return entry.Response.Code, err
	}

	// Entry is always saved, even if it is not public
	// This is to release the URL lock.
	if entry.isPublic {
		err := entry.setStorage(handler.Config)
		if err != nil {
			lock.Unlock()
			responseStatus.WithLabelValues(append([]string{hostname, fam, proto, "error"}, extraLabelValues...)...).Inc()
			fmt.Println("error %s \n", err)
			return 500, err
		}
	}

	handler.Cache.Put(r, entry)
	lock.Unlock()
	responseStatus.WithLabelValues(append([]string{hostname, fam, proto, cacheMiss}, extraLabelValues...)...).Inc()
	return handler.respond(w, entry, cacheMiss)
}
func host(r *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		if !strings.Contains(r.Host, ":") {
			return strings.ToLower(r.Host), nil
		}
		return "", err
	}
	return strings.ToLower(host), nil
}
func isIPv6(addr string) bool {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		// Strip away the port.
		addr = host
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.To4() == nil
}
