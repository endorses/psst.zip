package api_test

// X25519 base point: valid raw public key fixture. Encryption conformance is
// exercised by client crypto tests; backend tests treat envelopes as opaque.
const fixtureRecipientKey = "CQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const fixtureSlotJSON = `{"receive_protocol":2,"recipient_public_key":"CQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`

func fixtureSlotPolicy() map[string]any {
	return map[string]any{"receive_protocol": 2, "recipient_public_key": fixtureRecipientKey}
}
func fixtureReceiveEnvelope() []byte { return append([]byte("PSSTRCV2"), make([]byte, 108)...) }
