package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
)

// TODO Route graph should have paths for same host linked to host node. Host nodes should use binary search or hashing.
// TODO store all matches and pick highest priority
// TODO Strip port from both
// TODO prometheus statistics
// TODO Allow all HTTP methods
// TODO Allow capture groups and reusing variables in dst
// TODO Reverse proxy headers

const DEFAULT_ENDPOINT = ":8080"
const DEFAULT_ROUTE_FILE_PATH = "routes.json"
const DEFAULT_REDIRECT_STATUS = 302

var debug = false
var endpoint = DEFAULT_ENDPOINT
var routeFilePath = DEFAULT_ROUTE_FILE_PATH

type Route struct {
	Name           string `json:"name"`
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
	if success := parseCliArgs(); !success {
		return
	}

	if err := readRouteFile(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}

	if err := runHttpServer(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}
}

func parseCliArgs() bool {
	var showHelp bool
	flag.BoolVar(&showHelp, "help", false, "Show help.")
	flag.BoolVar(&debug, "debug", false, "Show extra debug messages.")
	flag.StringVar(&endpoint, "endpoint", DEFAULT_ENDPOINT, "The address-port endpoint to bind to.")
	flag.StringVar(&routeFilePath, "route-file", DEFAULT_ROUTE_FILE_PATH, "The path to the routes JSON config file.")
	// FIXME this returns?
	fmt.Print("Hello1\n")
	flag.Parse()
	fmt.Print("Hello2\n")

	// Show help and exit if requested
	if showHelp {
		flag.CommandLine.SetOutput(os.Stdout)
		flag.PrintDefaults()
		return false
	}

	return true
}

func readRouteFile() error {
	// Open file
	file, openErr := os.Open(routeFilePath)
	if openErr != nil {
		return fmt.Errorf("Failed to open route file: %v", openErr)
	}
	defer file.Close()

	// Read file
	data, readErr := io.ReadAll(file)
	if readErr != nil {
		return fmt.Errorf("Failed to read route file: %v", readErr)
	}

	// Parse routes
	// TODO extract the "routes" array in the root object
	parseErr := json.Unmarshal(data, &routes)
	if parseErr != nil {
		return fmt.Errorf("Failed to parse routes from file: %v", parseErr)
	}

	// Validate routes
	// TODO validate routes, scrap invalid ones, clear routes array

	// Compile source URLs
	// TODO
	// TODO ignore if error

	// Display info
	fmt.Printf("Loaded %v route(s).\n", len(routes))
	if debug {
		for i, route := range routes {
			fmt.Printf("Route %v:\n", i)
			fmt.Printf("  Name:            %v\n", route.Name)
			fmt.Printf("  Source URL:      %v\n", route.SourceUrl)
			fmt.Printf("  Destination URL: %v\n", route.DestinationUrl)
			fmt.Printf("  Priority:        %v\n", route.Priority)
			fmt.Printf("  Redirect status: %v\n", route.RedirectStatus)
			fmt.Printf("\n")
		}
	}

	return nil
}

func runHttpServer() error {
	http.HandleFunc("/", httpRouter)
	err := http.ListenAndServe(endpoint, nil)
	if err != nil {
		return fmt.Errorf("Error while running HTTP server: %v", err)
	}
	return nil
}

func httpRouter(response http.ResponseWriter, request *http.Request) {
	// Build source URL
	// TODO accept reverse proxy headers
	scheme := "http"
	url := fmt.Sprintf("%v://%v%v", scheme, request.Host, request.URL)

	// Find matching routes
	var bestRoute *CompiledRoute = nil
	for _, route := range routes {
		if route.CompiledSourceUrl.MatchString(url) {
			if bestRoute == nil || route.Priority > bestRoute.Priority {
				bestRoute = &route
			}
		}
	}

	// Check if match
	if bestRoute == nil {
		response.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(response, "Not found.\n")
		return
	}

	// Build destination URL
	// TODO insert variables
	destinationUrl := bestRoute.DestinationUrl

	// Redirect
	redirectStatus := bestRoute.RedirectStatus
	if redirectStatus == 0 {
		redirectStatus = DEFAULT_REDIRECT_STATUS
	}
	http.Redirect(response, request, destinationUrl, redirectStatus)
}
