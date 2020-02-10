// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package forwarder

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAPIEndpoint(t *testing.T) {
	testUS := "https://app.datadoghq.com"
	testEU := "https://app.datadoghq.eu"
	testOther := "https://foo.net"
	if getAPIValidationEndpoint(testUS) != "https://api.datadoghq.com" {
		t.Error("API validation is being done on https://app.datadoghq.com rather than https://api.datadoghq.com")
	}
	if getAPIValidationEndpoint(testEU) != "https://api.datadoghq.eu" {
		t.Error("API validation is being done on https://app.datadoghq.eu rather than https://api.datadoghq.eu")
	}
	if getAPIValidationEndpoint(testOther) != testOther {
		t.Errorf("API endpint for %s was changed during api endpoint assignment", testDomain)
	}
}
func TestHasValidAPIKey(t *testing.T) {
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts1.Close()
	defer ts2.Close()

	keysPerDomains := map[string][]string{
		ts1.URL: {"api_key1", "api_key2"},
		ts2.URL: {"key3"},
	}

	fh := forwarderHealth{keysPerDomains: keysPerDomains}
	fh.init()
	assert.True(t, fh.hasValidAPIKey())

	assert.Equal(t, &apiKeyValid, apiKeyStatus.Get("API key ending with _key1"))
	assert.Equal(t, &apiKeyValid, apiKeyStatus.Get("API key ending with _key2"))
	assert.Equal(t, &apiKeyValid, apiKeyStatus.Get("API key ending with key3"))
}

func TestHasValidAPIKeyErrors(t *testing.T) {
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("api_key") == "api_key1" {
			w.WriteHeader(http.StatusForbidden)
		} else if r.Form.Get("api_key") == "api_key2" {
			w.WriteHeader(http.StatusNotFound)
		} else {
			assert.Fail(t, fmt.Sprintf("Unknown api key received: %v", r.Form.Get("api_key")))
		}
	}))
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts1.Close()
	defer ts2.Close()

	keysPerDomains := map[string][]string{
		ts1.URL: {"api_key1", "api_key2"},
		ts2.URL: {"key3"},
	}

	fh := forwarderHealth{keysPerDomains: keysPerDomains}
	fh.init()
	assert.True(t, fh.hasValidAPIKey())

	assert.Equal(t, &apiKeyInvalid, apiKeyStatus.Get("API key ending with _key1"))
	assert.Equal(t, &apiKeyStatusUnknown, apiKeyStatus.Get("API key ending with _key2"))
	assert.Equal(t, &apiKeyValid, apiKeyStatus.Get("API key ending with key3"))
}
