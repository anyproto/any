package api

// AccountResponse is the body of GET /v1/account. Metadata is empty in
// v1 because the SDK does not yet expose the account's identityRepo
// metadata; the field is reserved so the shape doesn't change when it
// lands.
type AccountResponse struct {
	Id       string           `json:"id"`
	Metadata *AccountMetadata `json:"metadata,omitempty"`
}

// AccountMetadata mirrors space.AccountMetadata. Unused on the wire in
// v1 but pre-staged so the converter exists when PUT /v1/account/metadata
// is unblocked on the SDK side.
type AccountMetadata struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
}
