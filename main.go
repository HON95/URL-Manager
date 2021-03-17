package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const defaultEndpoint = ":8080"
const defaultRouteFilePath = "routes.json"
const defaultRedirectStatus = 302

var enableDebug = false
var enableRequestLogging = false
var endpoint = defaultEndpoint
var routeFilePath = defaultRouteFilePath
var metricsEndpoint = ""
var compiledRouteIDPattern = regexp.MustCompile(`^[0-9a-zA-Z-_]+$`)

var metricsInfoGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "url_manager_info",
	Help: "Metadata about the exporter.",
}, []string{"version"})
var metricsTotalCounter = promauto.NewCounter(prometheus.CounterOpts{
	Name: "url_manager_requests_total",
	Help: "The total number of received requests.",
})
var metricsNotFoundCounter = promauto.NewCounter(prometheus.CounterOpts{
	Name: "url_manager_not_found_total",
	Help: "The number of requests not matching any routes.",
})
var metricsRouteChosenCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "url_manager_route_chosen_total",
	Help: "The number of times a route has been chosen as the best match.",
}, []string{"route"})
var metricsRouteMalformedDestinationCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "url_manager_route_malformed_destination_total",
	Help: "The number of times a route has resulted in an invalid destination URL.",
}, []string{"route"})

type route struct {
	ID             string `json:"id"`
	SourceURL      string `json:"source_url"`
	DestinationURL string `json:"destination_url"`
	RedirectStatus int    `json:"redirect_status"`
	Priority       int    `json:"priority"`
}

type compiledRoute struct {
	route
	CompiledSourceURL *regexp.Regexp
}

var routes []compiledRoute

func main() {
	fmt.Printf("%v version %v by %v.\n\n", appName, appVersion, appAuthor)

	parseCliArgs()

	if err := readRouteFile(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}

	if err := runServers(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}
}

func parseCliArgs() {
	flag.BoolVar(&enableDebug, "debug", false, "Show debug messages.")
	flag.BoolVar(&enableRequestLogging, "log", false, "Log requests.")
	flag.StringVar(&endpoint, "endpoint", defaultEndpoint, "The address-port endpoint to bind to.")
	flag.StringVar(&routeFilePath, "route-file", defaultRouteFilePath, "The path to the routes JSON config file.")
	flag.StringVar(&metricsEndpoint, "metrics-endpoint", "", "Metrics address-port endpoint. Disabled if not set.")

	// Exits on error
	flag.Parse()
}

func readRouteFile() error {
	// Open file
	file, openErr := os.Open(routeFilePath)
	if openErr != nil {
		return fmt.Errorf("Failed to open route file (missing file?): \n%v", openErr)
	}
	defer file.Close()

	// Read file
	data, readErr := io.ReadAll(file)
	if readErr != nil {
		return fmt.Errorf("Failed to read route file (I/O error?): \n%v", readErr)
	}

	// Parse routes from file
	var rawRoutes []route
	parseErr := json.Unmarshal(data, &rawRoutes)
	if parseErr != nil {
		return fmt.Errorf("Failed to parse routes from file (malformed JSON file?): \n%v", parseErr)
	}

	// Validate and compile routes
	routes = make([]compiledRoute, 0)
	for i, rawRoute := range rawRoutes {
		if compiledRoute, err := compileRoute(&rawRoute); err == nil {
			routes = append(routes, compiledRoute)
		} else {
			fmt.Fprintf(os.Stderr, "Route #%v is malformed: %v\n", i, err)
		}
	}

	// Display info
	fmt.Printf("Loaded %v route(s).\n", len(routes))
	if enableDebug {
		for i, route := range routes {
			fmt.Printf("Route %v:\n", i)
			fmt.Printf("  Name:            %v\n", route.ID)
			fmt.Printf("  Source URL:      %v\n", route.SourceURL)
			fmt.Printf("  Destination URL: %v\n", route.DestinationURL)
			fmt.Printf("  Redirect status: %v\n", route.RedirectStatus)
			fmt.Printf("  Priority:        %v\n", route.Priority)
		}
	}

	return nil
}

func compileRoute(rawRoute *route) (compiledRoute, error) {
	var compiledRoute compiledRoute

	// ID
	if len(rawRoute.ID) == 0 || !compiledRouteIDPattern.MatchString(rawRoute.ID) {
		return compiledRoute, fmt.Errorf("Route ID contains illegal characters")
	}
	compiledRoute.ID = rawRoute.ID

	// Source URL
	if len(rawRoute.SourceURL) == 0 {
		return compiledRoute, fmt.Errorf("Missing source URL")
	}
	compiledRoute.SourceURL = rawRoute.SourceURL
	if result, err := regexp.Compile(rawRoute.SourceURL); err == nil {
		compiledRoute.CompiledSourceURL = result
	} else {
		return compiledRoute, fmt.Errorf("Route source URL regexp won't compile.\n%v", err)
	}

	// Destination URL
	// Postpone format check for after variable substitution
	if len(rawRoute.DestinationURL) == 0 {
		return compiledRoute, fmt.Errorf("Missing destination URL")
	}
	compiledRoute.DestinationURL = rawRoute.DestinationURL

	// Priority
	// Defaults to 0
	compiledRoute.Priority = rawRoute.Priority

	// Redirect status
	status := rawRoute.RedirectStatus
	switch {
	case status == 0:
		compiledRoute.RedirectStatus = defaultRedirectStatus
	case status >= 301 && status <= 308:
		compiledRoute.RedirectStatus = status
	default:
		return compiledRoute, fmt.Errorf("Invalid redirect status value")
	}

	return compiledRoute, nil
}

func runServers() error {
	metricsInfoGauge.With(prometheus.Labels{"version": appVersion}).Set(1)

	// Metrics server (async routine)
	if len(metricsEndpoint) > 0 {
		var metricsServeMux http.ServeMux
		metricsServeMux.Handle("/", promhttp.Handler())
		go func() {
			if err := http.ListenAndServe(metricsEndpoint, &metricsServeMux); err != nil {
				fmt.Fprintf(os.Stderr, "Error while running metrics HTTP server: %v", err)
			}
		}()
	}

	// Main server (blocking)
	var mainServeMux http.ServeMux
	mainServeMux.HandleFunc("/", handleMainRequest)
	if err := http.ListenAndServe(endpoint, &mainServeMux); err != nil {
		return fmt.Errorf("Error while running main HTTP server: %v", err)
	}

	return nil
}

func handleMainRequest(response http.ResponseWriter, request *http.Request) {
	metricsTotalCounter.Inc()

	// Get local or forwarded proto, domain and from-addr
	realFrom := request.RemoteAddr
	if forwardedFors := request.Header["X-Forwarded-For"]; len(forwardedFors) > 0 {
		realFrom = forwardedFors[0]
	}
	realProto := "http"
	if forwardedProtos := request.Header["X-Forwarded-Proto"]; len(forwardedProtos) > 0 {
		realProto = forwardedProtos[0]
	}
	realHost := request.Host
	if forwardedHosts := request.Header["X-Forwarded-Host"]; len(forwardedHosts) > 0 {
		realHost = forwardedHosts[0]
	}

	// Build source URL
	sourceURL := fmt.Sprintf("%v://%v%v", realProto, realHost, request.URL)

	// Find matching routes (linear search)
	var bestRouteID = -1
	for i, route := range routes {
		if route.CompiledSourceURL.MatchString(sourceURL) {
			if bestRouteID == -1 || route.Priority > routes[bestRouteID].Priority {
				bestRouteID = i
			}
		}
	}

	// Check if no matches
	if bestRouteID == -1 {
		http.Error(response, "404 Not found.\n", http.StatusNotFound)
		metricsNotFoundCounter.Inc()
		logRequest(realFrom, 404, "", sourceURL, "")
		return
	}

	// Build destination URL
	route := &routes[bestRouteID]
	metricsRouteChosenCounter.With(prometheus.Labels{"route": route.ID}).Inc()
	destinationURL := route.CompiledSourceURL.ReplaceAllString(sourceURL, route.DestinationURL)
	if _, err := url.ParseRequestURI(destinationURL); err != nil {
		if enableDebug {
			fmt.Fprintf(os.Stderr, "Malformed destination:\n")
			fmt.Fprintf(os.Stderr, "  Route: \"%v\"\n", route.ID)
			fmt.Fprintf(os.Stderr, "  Source URL: \"%v\"\n", sourceURL)
			fmt.Fprintf(os.Stderr, "  Destination URL (template): \"%v\"\n", route.DestinationURL)
			fmt.Fprintf(os.Stderr, "  Destination URL (actual): \"%v\"\n", destinationURL)
		}
		http.Error(response, "400 Malformed destination.\n", http.StatusBadRequest)
		metricsRouteMalformedDestinationCounter.With(prometheus.Labels{"route": route.ID}).Inc()
		logRequest(realFrom, 400, route.ID, sourceURL, "")
		return
	}

	// Redirect
	http.Redirect(response, request, destinationURL, route.RedirectStatus)
	logRequest(realFrom, route.RedirectStatus, route.ID, sourceURL, destinationURL)
}

func logRequest(clientAddr string, httpResult int, routeID string, sourceURL string, destinationURL string) {
	if enableRequestLogging {
		fmt.Printf("Request: client=\"%v\" status=\"%v\" route=\"%v\" source=\"%v\" destination=\"%v\"\n", clientAddr, httpResult, routeID, sourceURL, destinationURL)
	}
}
