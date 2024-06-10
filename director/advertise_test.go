/***************************************************************
 *
 * Copyright (C) 2024, Pelican Project, Morgridge Institute for Research
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you
 * may not use this file except in compliance with the License.  You may
 * obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 ***************************************************************/

package director

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pelicanplatform/pelican/server_structs"
	"github.com/pelicanplatform/pelican/utils"
)

var (
	//go:embed resources/mock_topology.json
	mockTopology string
)

func TestParseServerAdFromTopology(t *testing.T) {

	server := utils.Server{
		Endpoint:     "http://my-endpoint.com",
		AuthEndpoint: "https://my-auth-endpoint.com",
		Resource:     "MY_SERVER",
	}

	t.Run("test-setting-name", func(t *testing.T) {
		ad := parseServerAdFromTopology(server, server_structs.OriginType, server_structs.Capabilities{})
		assert.Equal(t, "MY_SERVER", ad.Name)
	})

	t.Run("parse-server-ads-with-scheme", func(t *testing.T) {
		ad := parseServerAdFromTopology(server, server_structs.OriginType, server_structs.Capabilities{})
		assert.Equal(t, "http://my-endpoint.com", ad.URL.String())
		assert.Equal(t, "https://my-auth-endpoint.com", ad.AuthURL.String())
	})

	t.Run("parse-server-ads-no-scheme", func(t *testing.T) {
		server.Endpoint = "my-endpoint.com"
		server.AuthEndpoint = "my-auth-endpoint.com"
		ad := parseServerAdFromTopology(server, server_structs.OriginType, server_structs.Capabilities{})
		assert.Equal(t, "http://my-endpoint.com", ad.URL.String())
		assert.Equal(t, "https://my-auth-endpoint.com", ad.AuthURL.String())
	})

	t.Run("test-ad-type", func(t *testing.T) {
		ad := parseServerAdFromTopology(server, server_structs.OriginType, server_structs.Capabilities{})
		assert.True(t, ad.Type == server_structs.OriginType)
		ad = parseServerAdFromTopology(server, server_structs.CacheType, server_structs.Capabilities{})
		assert.True(t, ad.Type == server_structs.CacheType)
	})
	t.Run("test-from-topology", func(t *testing.T) {
		ad := parseServerAdFromTopology(server, server_structs.OriginType, server_structs.Capabilities{})
		assert.True(t, ad.FromTopology)
		ad = parseServerAdFromTopology(server, server_structs.CacheType, server_structs.Capabilities{})
		assert.True(t, ad.FromTopology)
	})

	t.Run("test-caps-parsing", func(t *testing.T) {
		// Only testing the caps that also get set as top level fields
		caps := server_structs.Capabilities{
			Writes:      true,
			Listings:    true,
			DirectReads: true,
		}
		ad := parseServerAdFromTopology(server, server_structs.OriginType, caps)
		assert.True(t, ad.Writes)
		assert.True(t, ad.Caps.Writes)
		assert.True(t, ad.Listings)
		assert.True(t, ad.Caps.Listings)
		assert.True(t, ad.DirectReads)
		assert.True(t, ad.Caps.DirectReads)

		ad = parseServerAdFromTopology(server, server_structs.CacheType, caps)
		assert.False(t, ad.Writes)
		assert.False(t, ad.Caps.Writes)
		assert.False(t, ad.Listings)
		assert.False(t, ad.Caps.Listings)
		assert.False(t, ad.DirectReads)
		assert.False(t, ad.Caps.DirectReads)
	})

	t.Run("test-invalid-url", func(t *testing.T) {
		// Capture logs
		hook := logrustest.NewLocal(logrus.StandardLogger())
		defer hook.Reset()

		server.Endpoint = "http://a server "
		server.AuthEndpoint = "https://a different server "
		ad := parseServerAdFromTopology(server, server_structs.OriginType, server_structs.Capabilities{})
		assert.Empty(t, ad.URL.String())
		assert.Empty(t, ad.AuthURL.String())
		assert.Len(t, hook.AllEntries(), 2)
		assert.Equal(t, logrus.WarnLevel, hook.LastEntry().Level)
		assert.Contains(t, hook.Entries[0].Message, "invalid unauthenticated URL")
		assert.Contains(t, hook.Entries[1].Message, "invalid authenticated URL")
	})
}

func JSONHandler(w http.ResponseWriter, r *http.Request) {
	// Set the Content-Type header to indicate JSON.
	w.Header().Set("Content-Type", "application/json")

	// Write the JSON response to the response body.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(mockTopology))
}
func TestAdvertiseOSDF(t *testing.T) {
	viper.Reset()
	serverAds.DeleteAll()
	topoServer := httptest.NewServer(http.HandlerFunc(JSONHandler))
	defer topoServer.Close()
	viper.Set("Federation.TopologyNamespaceUrl", topoServer.URL)

	err := AdvertiseOSDF(context.Background())
	require.NoError(t, err)

	var foundServer server_structs.Advertisement
	for _, item := range serverAds.Items() {
		if item.Value().URL.Host == "origin1-endpoint.com" {
			foundServer = *item.Value()
		}
	}
	require.NotNil(t, foundServer)
	assert.True(t, foundServer.FromTopology)
	require.NotNil(t, foundServer.NamespaceAds)
	assert.True(t, foundServer.NamespaceAds[0].FromTopology)

	// Test a few values. If they're correct, it indicates the whole process likely succeeded
	nsAd, oAds, cAds := getAdsForPath("/my/server/path/to/file")
	assert.Equal(t, "/my/server", nsAd.Path)
	assert.Equal(t, uint(3), nsAd.Generation[0].MaxScopeDepth)
	assert.Equal(t, "https://origin1-auth-endpoint.com", oAds[0].AuthURL.String())
	assert.Equal(t, "https://cache2.com", cAds[0].URL.String())
	// Check that various capabilities have survived until this point. Because these are from topology,
	// origin and namespace caps should be the same
	assert.True(t, oAds[0].Writes)
	assert.True(t, oAds[0].Caps.Writes)
	assert.True(t, oAds[0].Listings)
	assert.True(t, oAds[0].Caps.Listings)
	assert.False(t, oAds[0].Caps.PublicReads)
	assert.True(t, nsAd.Caps.Writes)
	assert.True(t, nsAd.Caps.Listings)
	assert.False(t, nsAd.Caps.PublicReads)
	assert.True(t, nsAd.Caps.Listings)

	nsAd, oAds, cAds = getAdsForPath("/my/server/2/path/to/file")
	assert.Equal(t, "/my/server/2", nsAd.Path)
	assert.True(t, nsAd.PublicRead)
	assert.Equal(t, "https://origin2-auth-endpoint.com", oAds[0].AuthURL.String())
	assert.Equal(t, "http://cache-endpoint.com", cAds[0].URL.String())
}

func TestFindDownedTopologyCache(t *testing.T) {
	mockTopoCacheA := utils.Server{AuthEndpoint: "cacheA.org:8443", Endpoint: "cacheA.org:8000", Resource: "CACHE_A"}
	mockTopoCacheB := utils.Server{AuthEndpoint: "cacheB.org:8443", Endpoint: "cacheB.org:8000", Resource: "CACHE_B"}
	mockTopoCacheC := utils.Server{AuthEndpoint: "cacheC.org:8443", Endpoint: "cacheC.org:8000", Resource: "CACHE_C"}
	mockTopoCacheD := utils.Server{AuthEndpoint: "cacheD.org:8443", Endpoint: "cacheD.org:8000", Resource: "CACHE_D"}
	t.Run("empty-response", func(t *testing.T) {
		get := findDownedTopologyCache(
			[]utils.Server{},
			[]utils.Server{},
		)
		assert.Empty(t, get)
	})

	t.Run("no-downed-cache", func(t *testing.T) {
		get := findDownedTopologyCache(
			[]utils.Server{mockTopoCacheA, mockTopoCacheB, mockTopoCacheC, mockTopoCacheD},
			[]utils.Server{mockTopoCacheA, mockTopoCacheB, mockTopoCacheC, mockTopoCacheD},
		)
		assert.Empty(t, get)
	})

	t.Run("one-downed-cache", func(t *testing.T) {
		get := findDownedTopologyCache(
			[]utils.Server{mockTopoCacheA, mockTopoCacheB, mockTopoCacheC},
			[]utils.Server{mockTopoCacheA, mockTopoCacheB, mockTopoCacheC, mockTopoCacheD},
		)
		require.Len(t, get, 1)
		assert.EqualValues(t, mockTopoCacheD, get[0])
	})

	t.Run("two-downed-cache", func(t *testing.T) {
		get := findDownedTopologyCache(
			[]utils.Server{mockTopoCacheB, mockTopoCacheC},
			[]utils.Server{mockTopoCacheA, mockTopoCacheB, mockTopoCacheC, mockTopoCacheD},
		)
		require.Len(t, get, 2)
		assert.EqualValues(t, mockTopoCacheA, get[0])
		assert.EqualValues(t, mockTopoCacheD, get[1])
	})

	t.Run("all-downed-cache", func(t *testing.T) {
		get := findDownedTopologyCache(
			[]utils.Server{},
			[]utils.Server{mockTopoCacheA, mockTopoCacheB, mockTopoCacheC, mockTopoCacheD},
		)
		assert.EqualValues(t, []utils.Server{mockTopoCacheA, mockTopoCacheB, mockTopoCacheC, mockTopoCacheD}, get)
	})
}

func TestUpdateDowntimeFromTopology(t *testing.T) {
	mockTopoCacheA := utils.Server{AuthEndpoint: "cacheA.org:8443", Endpoint: "cacheA.org:8000", Resource: "CACHE_A"}
	mockTopoCacheB := utils.Server{AuthEndpoint: "cacheB.org:8443", Endpoint: "cacheB.org:8000", Resource: "CACHE_B"}
	mockTopoCacheC := utils.Server{AuthEndpoint: "cacheC.org:8443", Endpoint: "cacheC.org:8000", Resource: "CACHE_C"}

	t.Run("no-change-with-same-downtime", func(t *testing.T) {
		filteredServers = map[string]filterType{}
		updateDowntimeFromTopology(
			&utils.TopologyNamespacesJSON{},
			&utils.TopologyNamespacesJSON{Caches: []utils.Server{mockTopoCacheA, mockTopoCacheB}},
		)
		checkResult := func() {
			filteredServersMutex.RLock()
			defer filteredServersMutex.RUnlock()
			assert.Len(t, filteredServers, 2)
			require.NotEmpty(t, filteredServers[mockTopoCacheA.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheA.Resource])
			require.NotEmpty(t, filteredServers[mockTopoCacheB.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheB.Resource])
		}
		checkResult()

		// second round of updates
		updateDowntimeFromTopology(
			&utils.TopologyNamespacesJSON{},
			&utils.TopologyNamespacesJSON{Caches: []utils.Server{mockTopoCacheA, mockTopoCacheB}},
		)
		// Same result
		checkResult()
	})

	t.Run("one-server-back-online", func(t *testing.T) {
		filteredServers = map[string]filterType{}
		updateDowntimeFromTopology(
			&utils.TopologyNamespacesJSON{},
			&utils.TopologyNamespacesJSON{Caches: []utils.Server{mockTopoCacheA, mockTopoCacheB}},
		)
		func() {
			filteredServersMutex.RLock()
			defer filteredServersMutex.RUnlock()
			assert.Len(t, filteredServers, 2)
			require.NotEmpty(t, filteredServers[mockTopoCacheA.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheA.Resource])
			require.NotEmpty(t, filteredServers[mockTopoCacheB.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheB.Resource])
		}()

		// second round of updates
		updateDowntimeFromTopology(
			&utils.TopologyNamespacesJSON{Caches: []utils.Server{mockTopoCacheA}}, // A is back online
			&utils.TopologyNamespacesJSON{Caches: []utils.Server{mockTopoCacheA, mockTopoCacheB}},
		)

		func() {
			filteredServersMutex.RLock()
			defer filteredServersMutex.RUnlock()
			assert.Len(t, filteredServers, 1)
			require.NotEmpty(t, filteredServers[mockTopoCacheB.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheB.Resource])
		}()
	})

	t.Run("one-more-server-in-downtime", func(t *testing.T) {
		filteredServers = map[string]filterType{}
		updateDowntimeFromTopology(
			&utils.TopologyNamespacesJSON{},
			&utils.TopologyNamespacesJSON{Caches: []utils.Server{mockTopoCacheA, mockTopoCacheB}},
		)
		func() {
			filteredServersMutex.RLock()
			defer filteredServersMutex.RUnlock()
			assert.Len(t, filteredServers, 2)
			require.NotEmpty(t, filteredServers[mockTopoCacheA.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheA.Resource])
			require.NotEmpty(t, filteredServers[mockTopoCacheB.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheB.Resource])
		}()

		// second round of updates
		updateDowntimeFromTopology(
			&utils.TopologyNamespacesJSON{Caches: []utils.Server{}},
			&utils.TopologyNamespacesJSON{Caches: []utils.Server{mockTopoCacheA, mockTopoCacheB, mockTopoCacheC}},
		)

		func() {
			filteredServersMutex.RLock()
			defer filteredServersMutex.RUnlock()
			assert.Len(t, filteredServers, 3)
			require.NotEmpty(t, filteredServers[mockTopoCacheA.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheA.Resource])
			require.NotEmpty(t, filteredServers[mockTopoCacheB.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheB.Resource])
			require.NotEmpty(t, filteredServers[mockTopoCacheC.Resource])
			assert.Equal(t, topoFiltered, filteredServers[mockTopoCacheC.Resource])
		}()
	})
}
