package publish

import (
	"encoding/json"

	"github.com/mejango/croptop/internal/store"
)

// VersionsKey is the site JSON key listing the versions this machine
// published or took in, newest last. It is how a machine tells its own
// versions, which a host may still hold while a newer one uploads, from ones
// published elsewhere, an agent's post say, whatever their sequences:
// renewals raise this machine's sequence without the host, so sequences cannot
// tell them apart. It is not in planet.json (store.Site.Public).
const VersionsKey = "croptopVersions"

// maxVersions bounds the list. A host is never that many publishes behind.
const maxVersions = 64

func versionsOf(site *store.Site) []string {
	var out []string
	if raw, ok := site.Raw[VersionsKey]; ok {
		json.Unmarshal(raw, &out)
	}
	return out
}

// rememberVersion records c as one of this machine's versions.
func rememberVersion(site *store.Site, c string) {
	if c == "" {
		return
	}
	list := versionsOf(site)
	for _, v := range list {
		if v == c {
			return
		}
	}
	list = append(list, c)
	if len(list) > maxVersions {
		list = list[len(list)-maxVersions:]
	}
	b, _ := json.Marshal(list)
	if site.Raw == nil {
		site.Raw = map[string]json.RawMessage{}
	}
	site.Raw[VersionsKey] = b
}

// ownVersion says whether c is one of this machine's versions: its last
// publish, or one it published or took in before.
func ownVersion(site *store.Site, c string) bool {
	if c == "" {
		return false
	}
	if site.LastPublishedCID != nil && *site.LastPublishedCID == c {
		return true
	}
	for _, v := range versionsOf(site) {
		if v == c {
			return true
		}
	}
	return false
}

// rememberedAfter says whether this machine learned of version a after b: a
// version taken in, say, after b was made. An upload of b must not go over a,
// or it would drop what a added. Unknown versions say nothing.
func rememberedAfter(site *store.Site, a, b string) bool {
	ia, ib := -1, -1
	for i, v := range versionsOf(site) {
		switch v {
		case a:
			ia = i
		case b:
			ib = i
		}
	}
	return ib >= 0 && ia > ib
}
