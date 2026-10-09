package store

import "encoding/json"

// StorageNeedsReview identifies a legacy published site whose owner has not
// made the new storage choice. This is local UI state, never public metadata
// or an inferred hosting opt-in. New sites already save an explicit P2P choice.
func (s *Site) StorageNeedsReview() bool {
	if s.LastPublished == nil && (s.LastPublishedCID == nil || *s.LastPublishedCID == "") && s.IPNSSequence == 0 {
		return false
	}
	var choice string
	return json.Unmarshal(s.Raw[StorageKey], &choice) != nil || (choice != StorageP2P && choice != StorageHosted)
}
