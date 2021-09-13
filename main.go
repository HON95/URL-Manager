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
	"strings"

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

// Route is a matching for an incoming URL and an associated redirect.
type Route struct {
	ID                   string         `json:"id"`
	Disabled             bool           `json:"disabled"`
	SourceURL            string         `json:"source_url"`
	SourceScheme         string         `json:"source_scheme"`
	SourceHost           string         `json:"source_host"`
	SourcePort           string         `json:"source_port"`
	SourcePath           string         `json:"source_path"`
	SourceQuery          string         `json:"source_query"`
	DestinationURL       string         `json:"destination_url"`
	RedirectStatus       int            `json:"redirect_status"`
	Priority             int            `json:"priority"`
	CompiledSourceURL    *regexp.Regexp `json:"-"`
	CompiledSourceScheme *regexp.Regexp `json:"-"`
	CompiledSourceHost   *regexp.Regexp `json:"-"`
	CompiledSourcePort   *regexp.Regexp `json:"-"`
	CompiledSourcePath   *regexp.Regexp `json:"-"`
	CompiledSourceQuery  *regexp.Regexp `json:"-"`
}

// List of all routes
var routes []*Route

// Tree of composite source routes (group on same raw regexes)
var compositeRoutes map[string]*schemeRouteGroup

type schemeRouteGroup struct {
	compiledScheme *regexp.Regexp
	hostRoutes     map[string]*hostRouteGroup
}

type hostRouteGroup struct {
	compiledHost *regexp.Regexp
	portRoutes   map[string]*portRouteGroup
}

type portRouteGroup struct {
	compiledPort *regexp.Regexp
	pathRoutes   map[string]*pathRouteGroup
}

type pathRouteGroup struct {
	compiledPath *regexp.Regexp
	queryRoutes  map[string]*queryRouteGroup
}

type queryRouteGroup struct {
	compiledQuery *regexp.Regexp
	routes        []*Route
}

// List of URL source routes
var urlRoutes map[string]*urlRouteGroup

type urlRouteGroup struct {
	compiledURL *regexp.Regexp
	routes      []*Route
}

func main() {
	fmt.Printf("%v version %v by %v.\n\n", appName, appVersion, appAuthor)

	// Init global data structures
	compositeRoutes = make(map[string]*schemeRouteGroup)
	urlRoutes = make(map[string]*urlRouteGroup)

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
	parseErr := json.Unmarshal(data, &routes)
	if parseErr != nil {
		return fmt.Errorf("Failed to parse routes from file (malformed JSON file?): \n%v", parseErr)
	}

	// Load routes by compiling regexes and inserting into data structures
	for i, route := range routes {
		if route.Disabled {
			if enableDebug {
				fmt.Printf("Skipping disabled route \"%v\".\n", route.ID)
			}
			continue
		}
		if err := loadRoute(route); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load route #%v: %v\n", i, err)
		}
	}

	// Display info
	fmt.Printf("Loaded %v route(s).\n", len(routes))
	if enableDebug {
		for i, route := range routes {
			fmt.Printf("Route %v:\n", i)
			fmt.Printf("  Name:            %v\n", route.ID)
			if route.SourceURL != "" {
				fmt.Printf("  Source URL:      %v\n", route.SourceURL)
			} else {
				fmt.Printf("  Source scheme:   %v\n", route.SourceScheme)
				fmt.Printf("  Source host:     %v\n", route.SourceHost)
				fmt.Printf("  Source port:     %v\n", route.SourcePort)
				fmt.Printf("  Source path:     %v\n", route.SourcePath)
				fmt.Printf("  Source query:    %v\n", route.SourceQuery)
			}
			fmt.Printf("  Destination URL: %v\n", route.DestinationURL)
			fmt.Printf("  Redirect status: %v\n", route.RedirectStatus)
			fmt.Printf("  Priority:        %v\n", route.Priority)
		}
	}

	return nil
}

func loadRoute(route *Route) error {
	// ID
	if route.ID == "" || !compiledRouteIDPattern.MatchString(route.ID) {
		return fmt.Errorf("Route ID contains illegal characters")
	}

	// Destination URL
	// Postpone URL format check for after variable substitution
	if route.DestinationURL == "" {
		return fmt.Errorf("Missing destination URL")
	}

	// Redirect status
	status := &route.RedirectStatus
	if *status == 0 {
		*status = defaultRedirectStatus
	} else if *status < 300 || *status > 399 {
		return fmt.Errorf("Invalid redirect status value")
	}

	// Source URL or composite
	hasSourceURL := route.SourceURL != ""
	hasSourceComposite := route.SourceScheme != "" || route.SourceHost != "" || route.SourcePort != "" || route.SourcePath != "" || route.SourceQuery != ""
	if !hasSourceURL && !hasSourceComposite {
		return fmt.Errorf("Missing source URL or composite")
	}
	if hasSourceURL && hasSourceComposite {
		return fmt.Errorf("Route can't contain both a source URL and any of the source composite fields")
	}

	if hasSourceURL {
		var urlGroup *urlRouteGroup
		if group, ok := urlRoutes[route.SourceURL]; ok {
			urlGroup = group
		} else {
			urlGroup = &urlRouteGroup{}
			if result, err := regexp.Compile(route.SourceURL); err == nil {
				urlGroup.compiledURL = result
			} else {
				return fmt.Errorf("Route source URL regexp won't compile.\n%v", err)
			}
			urlGroup.routes = make([]*Route, 0)
			urlRoutes[route.SourceURL] = urlGroup
		}
		route.CompiledSourceURL = urlGroup.compiledURL
		urlGroup.routes = append(urlGroup.routes, route)
	} else {
		// Scheme
		var schemeGroup *schemeRouteGroup
		if group, ok := compositeRoutes[route.SourceScheme]; ok {
			schemeGroup = group
		} else {
			schemeGroup = &schemeRouteGroup{}
			if result, err := regexp.Compile(route.SourceScheme); err == nil {
				schemeGroup.compiledScheme = result
			} else {
				return fmt.Errorf("Route source scheme regexp won't compile.\n%v", err)
			}
			schemeGroup.hostRoutes = make(map[string]*hostRouteGroup)
			compositeRoutes[route.SourceScheme] = schemeGroup
		}
		route.CompiledSourceScheme = schemeGroup.compiledScheme

		// Host
		var hostGroup *hostRouteGroup
		if group, ok := schemeGroup.hostRoutes[route.SourceHost]; ok {
			hostGroup = group
		} else {
			hostGroup = &hostRouteGroup{}
			if result, err := regexp.Compile(route.SourceHost); err == nil {
				hostGroup.compiledHost = result
			} else {
				return fmt.Errorf("Route source host regexp won't compile.\n%v", err)
			}
			hostGroup.portRoutes = make(map[string]*portRouteGroup)
			schemeGroup.hostRoutes[route.SourceHost] = hostGroup
		}
		route.CompiledSourceHost = hostGroup.compiledHost

		// Port
		var portGroup *portRouteGroup
		if group, ok := hostGroup.portRoutes[route.SourcePort]; ok {
			portGroup = group
		} else {
			portGroup = &portRouteGroup{}
			if result, err := regexp.Compile(route.SourcePort); err == nil {
				portGroup.compiledPort = result
			} else {
				return fmt.Errorf("Route source port regexp won't compile.\n%v", err)
			}
			portGroup.pathRoutes = make(map[string]*pathRouteGroup)
			hostGroup.portRoutes[route.SourcePort] = portGroup
		}
		route.CompiledSourcePort = portGroup.compiledPort

		// Path
		var pathGroup *pathRouteGroup
		if group, ok := portGroup.pathRoutes[route.SourcePath]; ok {
			pathGroup = group
		} else {
			pathGroup = &pathRouteGroup{}
			if result, err := regexp.Compile(route.SourcePath); err == nil {
				pathGroup.compiledPath = result
			} else {
				return fmt.Errorf("Route source path regexp won't compile.\n%v", err)
			}
			pathGroup.queryRoutes = make(map[string]*queryRouteGroup)
			portGroup.pathRoutes[route.SourcePath] = pathGroup
		}
		route.CompiledSourcePath = pathGroup.compiledPath

		// Query
		var queryGroup *queryRouteGroup
		if group, ok := pathGroup.queryRoutes[route.SourceQuery]; ok {
			queryGroup = group
		} else {
			queryGroup = &queryRouteGroup{}
			if result, err := regexp.Compile(route.SourceQuery); err == nil {
				queryGroup.compiledQuery = result
			} else {
				return fmt.Errorf("Route source query regexp won't compile.\n%v", err)
			}
			queryGroup.routes = make([]*Route, 0)
			pathGroup.queryRoutes[route.SourceQuery] = queryGroup
		}
		route.CompiledSourceQuery = queryGroup.compiledQuery

		queryGroup.routes = append(queryGroup.routes, route)
	}

	return nil
}

func runServers() error {
	metricsInfoGauge.With(prometheus.Labels{"version": appVersion}).Set(1)

	// Metrics server (async routine)
	if len(metricsEndpoint) > 0 {
		var metricsServeMux http.ServeMux
		metricsServeMux.Handle("/metrics", promhttp.Handler())
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

	// Find matching route
	route := findBestRoute(&sourceURL)
	if route == nil {
		http.Error(response, "404 Not found.\n", http.StatusNotFound)
		metricsNotFoundCounter.Inc()
		logRequest(realFrom, 404, "", sourceURL, "")
		return
	}
	metricsRouteChosenCounter.With(prometheus.Labels{"route": route.ID}).Inc()

	// Build destination URL
	// TODO require all to be named?
	// destinationURL := route.CompiledSourceURL.ReplaceAllString(sourceURL, route.DestinationURL)
	// TODO for url and composite
	// TODO for each non-nil pattern
	varMatches := make(map[string]string)
	varCaptures := route.CompiledSourceURL.FindStringSubmatch(sourceURL)
	varCaptureNames := route.CompiledSourceURL.SubexpNames()
	for i := range varCaptures {
		if i > 0 {
			if varCaptureNames[i] != "" {
				varMatches[varCaptureNames[i]] = varCaptures[i]
			}
		}
	}

	// TODO
	varOldNewPairs := make([]string, 0)
	for key, value := range varMatches {
		varRepr := fmt.Sprintf("${%v}", key)
		varOldNewPairs = append(varOldNewPairs, varRepr)
		varOldNewPairs = append(varOldNewPairs, value)
		fmt.Printf("REPLACE \"%v\" WITH \"%v\"\n", varRepr, value)
	}
	varReplacer := strings.NewReplacer(varOldNewPairs...)
	destinationURL := varReplacer.Replace(route.DestinationURL)

	// TODO
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

func findBestRoute(sourceURL *string) *Route {
	var bestRoute *Route

	// Check composite routes
	// TODO implement
	// TODO "" means any

	// Check URL routes
	for _, urlGroup := range urlRoutes {
		if urlGroup.compiledURL.MatchString(*sourceURL) {
			for _, route := range urlGroup.routes {
				if bestRoute == nil || route.Priority > bestRoute.Priority {
					bestRoute = route
				}
			}
		}
	}

	return bestRoute
}

func logRequest(clientAddr string, httpResult int, routeID string, sourceURL string, destinationURL string) {
	if enableRequestLogging {
		fmt.Printf("Request: client=\"%v\" status=\"%v\" route=\"%v\" source=\"%v\" destination=\"%v\"\n", clientAddr, httpResult, routeID, sourceURL, destinationURL)
	}
}
