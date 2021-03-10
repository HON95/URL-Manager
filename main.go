package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// TODO Route graph should have paths for same host linked to host node. Host nodes should use binary search or hashing.
// TODO store all matches and pick highest priority
// TODO Strip port from both

const ENDPOINT = ":8080"

type Route struct {
	host           string
	pathRegex      *regexp.Regexp
	destinationUrl string
	priority       int
}

var DEMO_ROUTES = []Route{
	{"localhost", regexp.MustCompile(`^/a.*/b$`), "/TODO", 0},
}

func main() {
	http.HandleFunc("/", router)

	fmt.Printf("Server ready: %s\n", ENDPOINT)
	err := http.ListenAndServe(ENDPOINT, nil)
	if err != nil {
		fmt.Printf("Server error: %s\n", err)
	}
}

func router(response http.ResponseWriter, request *http.Request) {
	// Only GET allowed
	if request.Method != http.MethodGet {
		response.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprintf(response, "Method not allowed.\n")
		return
	}

	// Discard port if present
	host := strings.Split(request.Host, ":")[0]

	// Find matching routes
	var bestRoute *Route = nil
	for _, route := range DEMO_ROUTES {
		if host == route.host && route.pathRegex.MatchString(request.URL.Path) {
			if bestRoute == nil || route.priority > bestRoute.priority {
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

	// Redirect
	http.Redirect(response, request, bestRoute.destinationUrl, 302)
}
