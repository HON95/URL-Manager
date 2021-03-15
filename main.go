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

const DEFAULT_ENDPOINT = ":8080"
const DEFAULT_ROUTE_FILE_PATH = "routes.json"
const DEFAULT_REDIRECT_STATUS = 302

var debug = false
var endpoint = DEFAULT_ENDPOINT
var routeFilePath = DEFAULT_ROUTE_FILE_PATH
var metricsPath = ""
var compiledRouteIdPattern = regexp.MustCompile(`^[0-9a-zA-Z-_]+$`)

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

type Route struct {
	Id             string `json:"id"`
	SourceUrl      string `json:"source_url"`
	DestinationUrl string `json:"destination_url"`
	Priority       int    `json:"priority"`
	RedirectStatus int    `json:"redirect_status"`
}

type CompiledRoute struct {
	Route
	CompiledSourceUrl *regexp.Regexp
}

var routes []CompiledRoute

func main() {
	parseCliArgs()

	if err := readRouteFile(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}

	if err := runHttpServer(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}
}

func parseCliArgs() {
	flag.BoolVar(&debug, "debug", false, "Show extra debug messages.")
	flag.StringVar(&metricsPath, "metrics", "", "Metrics endpoint. Disabled if not set.")
	flag.StringVar(&endpoint, "endpoint", DEFAULT_ENDPOINT, "The address-port endpoint to bind to.")
	flag.StringVar(&routeFilePath, "route-file", DEFAULT_ROUTE_FILE_PATH, "The path to the routes JSON config file.")

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
	var rawRoutes []Route
	parseErr := json.Unmarshal(data, &rawRoutes)
	if parseErr != nil {
		return fmt.Errorf("Failed to parse routes from file (malformed JSON file?): \n%v", parseErr)
	}

	// Validate and compile routes
	routes = make([]CompiledRoute, 0)
	for i, rawRoute := range rawRoutes {
		if compiledRoute, err := compileRoute(&rawRoute); err == nil {
			routes = append(routes, compiledRoute)
		} else {
			fmt.Fprintf(os.Stderr, "Route #%v is malformed: %v\n", i, err)
		}
	}

	// Display info
	fmt.Printf("Loaded %v route(s).\n", len(routes))
	if debug {
		for i, route := range routes {
			fmt.Printf("Route %v:\n", i)
			fmt.Printf("  Name:            %v\n", route.Id)
			fmt.Printf("  Source URL:      %v\n", route.SourceUrl)
			fmt.Printf("  Destination URL: %v\n", route.DestinationUrl)
			fmt.Printf("  Priority:        %v\n", route.Priority)
			fmt.Printf("  Redirect status: %v\n", route.RedirectStatus)
		}
	}

	return nil
}

func compileRoute(rawRoute *Route) (CompiledRoute, error) {
	var compiledRoute CompiledRoute

	// ID
	if len(rawRoute.Id) == 0 || !compiledRouteIdPattern.MatchString(rawRoute.Id) {
		return compiledRoute, fmt.Errorf("Route ID contains illegal characters.")
	}
	compiledRoute.Id = rawRoute.Id

	// Source URL
	if len(rawRoute.SourceUrl) == 0 {
		return compiledRoute, fmt.Errorf("Missing source URL.")
	}
	compiledRoute.SourceUrl = rawRoute.SourceUrl
	if result, err := regexp.Compile(rawRoute.SourceUrl); err == nil {
		compiledRoute.CompiledSourceUrl = result
	} else {
		return compiledRoute, fmt.Errorf("Route source URL regexp won't compile.\n%v", err)
	}

	// Destination URL
	// Postpone format check for after variable substitution
	if len(rawRoute.DestinationUrl) == 0 {
		return compiledRoute, fmt.Errorf("Missing destination URL.")
	}
	compiledRoute.DestinationUrl = rawRoute.DestinationUrl

	// Priority
	// Defaults to 0
	compiledRoute.Priority = rawRoute.Priority

	// Redirect status
	status := rawRoute.RedirectStatus
	switch {
	case status == 0:
		compiledRoute.RedirectStatus = DEFAULT_REDIRECT_STATUS
	case status >= 301 && status <= 308:
		compiledRoute.RedirectStatus = status
	default:
		return compiledRoute, fmt.Errorf("Invalid redirect status value.")
	}

	return compiledRoute, nil
}

func runHttpServer() error {
	http.HandleFunc("/", handleRequest)
	if len(metricsPath) > 0 {
		fmt.Printf("Enabling metrics on \"%v\".\n", metricsPath)
		http.Handle(metricsPath, promhttp.Handler())
	}
	if err := http.ListenAndServe(endpoint, nil); err != nil {
		return fmt.Errorf("Error while running HTTP server: %v", err)
	}

	return nil
}

func handleRequest(response http.ResponseWriter, request *http.Request) {
	metricsTotalCounter.Inc()

	// Build source URL
	// TODO accept reverse proxy headers
	scheme := "http"
	sourceUrl := fmt.Sprintf("%v://%v%v", scheme, request.Host, request.URL)
	if debug {
		fmt.Printf("Request: url=\"%v\"\n", sourceUrl)
	}

	// Find matching routes (linear search)
	var bestRouteId = -1
	for i, route := range routes {
		if route.CompiledSourceUrl.MatchString(sourceUrl) {
			if debug {
				fmt.Printf("Potential match: name=\"%v\" priority=\"%v\" url=\"%v\"\n", route.Id, route.Priority, route.DestinationUrl)
			}
			if bestRouteId == -1 || route.Priority > routes[bestRouteId].Priority {
				bestRouteId = i
			}
		}
	}

	// Check if no matches
	if bestRouteId == -1 {
		metricsNotFoundCounter.Inc()
		if debug {
			fmt.Printf("No matches.\n")
		}
		response.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(response, "Not found.\n")
		return
	}

	// Build destination URL
	route := &routes[bestRouteId]
	metricsRouteChosenCounter.With(prometheus.Labels{"route": route.Id}).Inc()
	destinationUrl := route.CompiledSourceUrl.ReplaceAllString(sourceUrl, route.DestinationUrl)
	if _, err := url.ParseRequestURI(destinationUrl); err != nil {
		metricsRouteMalformedDestinationCounter.With(prometheus.Labels{"route": route.Id}).Inc()
		if debug {
			fmt.Printf("Error: Malformed destination URL.\n")
		}
		response.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(response, "Malformed destination.\n")
		return
	}
	if debug {
		fmt.Printf("Result: name=\"%v\" status=\"%v\" url=\"%v\"\n", route.Id, route.RedirectStatus, destinationUrl)
	}

	// Redirect
	http.Redirect(response, request, destinationUrl, route.RedirectStatus)
}
