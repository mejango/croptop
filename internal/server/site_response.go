package server

import (
	"encoding/json"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/store"
)

// siteResponse keeps the canonical address rule in gateway, including when
// the browser displays a site from the list response rather than its URL route.
func siteResponse(site *store.Site) map[string]json.RawMessage {
	data, _ := json.Marshal(site)
	var response map[string]json.RawMessage
	json.Unmarshal(data, &response)
	response["croptopURL"], _ = json.Marshal(gateway.URL(site))
	response[store.StorageKey], _ = json.Marshal(site.StorageMode())
	response["croptopStorageNeedsReview"], _ = json.Marshal(site.StorageNeedsReview())
	return response
}
