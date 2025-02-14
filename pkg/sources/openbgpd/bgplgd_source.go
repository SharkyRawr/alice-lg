package openbgpd

import (
	"context"
	"net/http"
	"time"

	"github.com/alice-lg/alice-lg/pkg/api"
	"github.com/alice-lg/alice-lg/pkg/caches"
	"github.com/alice-lg/alice-lg/pkg/decoders"
)

const (
	// BgplgdSourceVersion is currently fixed at 1.0
	BgplgdSourceVersion = "1.0"
)

// BgplgdSource implements a source for Alice, consuming
// the openbgp bgplgd.
type BgplgdSource struct {
	// cfg is the source configuration retrieved
	// from the alice config file.
	cfg *Config

	// Store the neighbor responses from the server here
	neighborsCache        *caches.NeighborsCache
	neighborsSummaryCache *caches.NeighborsCache

	// Store the routes responses from the server
	// here identified by neighborID
	routesCache         *caches.RoutesCache
	routesReceivedCache *caches.RoutesCache
	routesFilteredCache *caches.RoutesCache
}

// NewBgplgdSource creates a new source instance with a configuration.
func NewBgplgdSource(cfg *Config) *BgplgdSource {
	cacheDisabled := cfg.CacheTTL == 0

	// Initialize caches
	nc := caches.NewNeighborsCache(cacheDisabled)
	nsc := caches.NewNeighborsCache(cacheDisabled)
	rc := caches.NewRoutesCache(cacheDisabled, cfg.RoutesCacheSize)
	rrc := caches.NewRoutesCache(cacheDisabled, cfg.RoutesCacheSize)
	rfc := caches.NewRoutesCache(cacheDisabled, cfg.RoutesCacheSize)

	return &BgplgdSource{
		cfg:                   cfg,
		neighborsCache:        nc,
		neighborsSummaryCache: nsc,
		routesCache:           rc,
		routesReceivedCache:   rrc,
		routesFilteredCache:   rfc,
	}
}

// ExpireCaches ... will flush the cache.
func (src *BgplgdSource) ExpireCaches() int {
	totalExpired := src.routesReceivedCache.Expire()
	return totalExpired
}

// Requests
// ========

// ShowNeighborsRequest makes an all neighbors request
func (src *BgplgdSource) ShowNeighborsRequest(ctx context.Context) (*http.Request, error) {
	url := src.cfg.APIURL("/neighbors")
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

// ShowNeighborsSummaryRequest builds an neighbors status request
func (src *BgplgdSource) ShowNeighborsSummaryRequest(
	ctx context.Context,
) (*http.Request, error) {
	url := src.cfg.APIURL("/summary")
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

// ShowNeighborRIBRequest retrives the routes accepted from the neighbor
// identified by bgp-id.
func (src *BgplgdSource) ShowNeighborRIBRequest(
	ctx context.Context,
	neighborID string,
) (*http.Request, error) {
	url := src.cfg.APIURL("/rib?neighbor=%s", neighborID)
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

// ShowRIBRequest makes a request for retrieving all routes imported
// from all peers
func (src *BgplgdSource) ShowRIBRequest(ctx context.Context) (*http.Request, error) {
	url := src.cfg.APIURL("/rib")
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

// Datasource
// ==========

// makeResponseMeta will create a new api status with cache infos
func (src *BgplgdSource) makeResponseMeta() *api.Meta {
	return &api.Meta{
		CacheStatus: api.CacheStatus{
			CachedAt: time.Now().UTC(),
		},
		Version:         BgplgdSourceVersion,
		ResultFromCache: false,
		TTL:             time.Now().UTC().Add(src.cfg.CacheTTL),
	}
}

// Status returns an API status response. In our case
// this is pretty much only that the service is available.
func (src *BgplgdSource) Status() (*api.StatusResponse, error) {
	// Make API request and read response. We do not cache the result.
	response := &api.StatusResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Status: api.Status{
			Version: "openbgpd",
			Message: "openbgpd up and running",
		},
	}
	return response, nil
}

// Neighbors retrievs a full list of all neighbors
func (src *BgplgdSource) Neighbors() (*api.NeighborsResponse, error) {
	// Query cache and see if we have a hit
	response := src.neighborsCache.Get()
	if response != nil {
		response.Meta.ResultFromCache = true
		return response, nil
	}

	// Make API request and read response
	req, err := src.ShowNeighborsRequest(context.Background())
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := decoders.ReadJSONResponse(res)
	if err != nil {
		return nil, err
	}

	nb, err := decodeNeighbors(body)
	if err != nil {
		return nil, err
	}
	// Set route server id (sourceID) for all neighbors and
	// calculate the filtered routes.
	for _, n := range nb {
		n.RouteServerID = src.cfg.ID
		rejectedRes, err := src.RoutesFiltered(n.ID)
		if err != nil {
			return nil, err
		}
		rejectCount := len(rejectedRes.Filtered)
		n.RoutesFiltered = rejectCount

	}
	response = &api.NeighborsResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Neighbors: nb,
	}
	src.neighborsCache.Set(response)

	return response, nil
}

// NeighborsSummary retrievs list of neighbors, which
// might lack details like with number of rejected routes.
// It is much faster though.
func (src *BgplgdSource) NeighborsSummary() (*api.NeighborsResponse, error) {
	// Query cache and see if we have a hit
	response := src.neighborsSummaryCache.Get()
	if response != nil {
		response.Meta.ResultFromCache = true
		return response, nil
	}

	// Make API request and read response
	req, err := src.ShowNeighborsRequest(context.Background())
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := decoders.ReadJSONResponse(res)
	if err != nil {
		return nil, err
	}

	nb, err := decodeNeighbors(body)
	if err != nil {
		return nil, err
	}
	// Set route server id (sourceID) for all neighbors and
	// calculate the filtered routes.
	for _, n := range nb {
		n.RouteServerID = src.cfg.ID

	}
	response = &api.NeighborsResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Neighbors: nb,
	}
	src.neighborsSummaryCache.Set(response)
	return response, nil
}

// NeighborsStatus retrives the status summary
// for all neightbors
func (src *BgplgdSource) NeighborsStatus() (*api.NeighborsStatusResponse, error) {
	// Make API request and read response
	req, err := src.ShowNeighborsSummaryRequest(context.Background())
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Read and decode response
	body, err := decoders.ReadJSONResponse(res)
	if err != nil {
		return nil, err
	}

	nb, err := decodeNeighborsStatus(body)
	if err != nil {
		return nil, err
	}

	response := &api.NeighborsStatusResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Neighbors: nb,
	}
	return response, nil
}

// Routes retrieves the routes for a specific neighbor
// identified by ID.
func (src *BgplgdSource) Routes(neighborID string) (*api.RoutesResponse, error) {
	response := src.routesCache.Get(neighborID)
	if response != nil {
		response.Meta.ResultFromCache = true
		return response, nil
	}

	// Query RIB for routes received
	req, err := src.ShowNeighborRIBRequest(context.Background(), neighborID)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Read and decode response
	body, err := decoders.ReadJSONResponse(res)
	if err != nil {
		return nil, err
	}

	routes, err := decodeRoutes(body)
	if err != nil {
		return nil, err
	}

	// Filtered routes are marked with a large BGP community
	// as defined in the reject reasons.
	received := filterReceivedRoutes(src.cfg.RejectCommunities, routes)
	rejected := filterRejectedRoutes(src.cfg.RejectCommunities, routes)

	response = &api.RoutesResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Imported:    received,
		NotExported: api.Routes{},
		Filtered:    rejected,
	}
	src.routesCache.Set(neighborID, response)

	return response, nil
}

// RoutesReceived returns the routes exported by the neighbor.
func (src *BgplgdSource) RoutesReceived(neighborID string) (*api.RoutesResponse, error) {
	response := src.routesReceivedCache.Get(neighborID)
	if response != nil {
		response.Meta.ResultFromCache = true
		return response, nil
	}

	// Query RIB for routes received
	req, err := src.ShowNeighborRIBRequest(context.Background(), neighborID)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Read and decode response
	body, err := decoders.ReadJSONResponse(res)
	if err != nil {
		return nil, err
	}

	routes, err := decodeRoutes(body)
	if err != nil {
		return nil, err
	}

	// Filtered routes are marked with a large BGP community
	// as defined in the reject reasons.
	received := filterReceivedRoutes(src.cfg.RejectCommunities, routes)

	response = &api.RoutesResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Imported:    received,
		NotExported: api.Routes{},
		Filtered:    api.Routes{},
	}
	src.routesReceivedCache.Set(neighborID, response)

	return response, nil
}

// RoutesFiltered retrieves the routes filtered / not valid
func (src *BgplgdSource) RoutesFiltered(neighborID string) (*api.RoutesResponse, error) {
	response := src.routesFilteredCache.Get(neighborID)
	if response != nil {
		response.Meta.ResultFromCache = true
		return response, nil
	}

	// Query RIB for routes received
	req, err := src.ShowNeighborRIBRequest(context.Background(), neighborID)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Read and decode response
	body, err := decoders.ReadJSONResponse(res)
	if err != nil {
		return nil, err
	}

	routes, err := decodeRoutes(body)
	if err != nil {
		return nil, err
	}

	// Filtered routes are marked with a large BGP community
	// as defined in the reject reasons.
	rejected := filterRejectedRoutes(src.cfg.RejectCommunities, routes)

	response = &api.RoutesResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Imported:    api.Routes{},
		NotExported: api.Routes{},
		Filtered:    rejected,
	}
	src.routesFilteredCache.Set(neighborID, response)

	return response, nil
}

// RoutesNotExported retrievs the routes not exported
// from the rs for a neighbor.
func (src *BgplgdSource) RoutesNotExported(neighborID string) (*api.RoutesResponse, error) {
	response := &api.RoutesResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Imported:    api.Routes{},
		NotExported: api.Routes{},
		Filtered:    api.Routes{},
	}
	return response, nil
}

// AllRoutes retrievs the entire RIB from the source. This is never
// cached as it is processed by the store.
func (src *BgplgdSource) AllRoutes() (*api.RoutesResponse, error) {
	req, err := src.ShowRIBRequest(context.Background())
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Read and decode response
	body, err := decoders.ReadJSONResponse(res)
	if err != nil {
		return nil, err
	}

	routes, err := decodeRoutes(body)
	if err != nil {
		return nil, err
	}

	// Filtered routes are marked with a large BGP community
	// as defined in the reject reasons.
	received := filterReceivedRoutes(src.cfg.RejectCommunities, routes)
	rejected := filterRejectedRoutes(src.cfg.RejectCommunities, routes)

	response := &api.RoutesResponse{
		Response: api.Response{
			Meta: src.makeResponseMeta(),
		},
		Imported:    received,
		NotExported: api.Routes{},
		Filtered:    rejected,
	}
	return response, nil
}
