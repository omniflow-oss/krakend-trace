package main

import (
    "context"
    "net/http"
)

// ClientRegisterer is the symbol KrakenD looks for.
var ClientRegisterer = registerer("test-plugin")

type registerer string

func (r registerer) RegisterClients(register func(name string, handler func(context.Context, map[string]interface{}) (http.Handler, error))) {
    register(string(r), client)
}

func client(_ context.Context, _ map[string]interface{}) (http.Handler, error) {
    return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
        req.Header.Set("X-Test-Plugin", "true")
        http.DefaultTransport.RoundTrip(req) // forward
    }), nil
}