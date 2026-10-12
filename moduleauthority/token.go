package moduleauthority

import corework "github.com/codefly-dev/core/workcontext"

// Every Work Context this package carries — a capability the host minted for
// the module, or a parent a caller hands in — is an opaque string: the module
// is its holder and sender, never its verifier. sealed is the one question a
// holder may ask of one, through core's own reader: is it structurally a
// sealed capability in core's encoding? A token in any other encoding (the
// pre-cutover JSON payload among them) or of any other shape is refused here,
// before it is cached or sent, rather than by the receiver after.
//
// It authenticates nothing: no signature, issuer, audience, window or live
// state is checked, and no claim is read back as a finding. The receiver
// verifies.
func sealed(encoded string) bool {
	if encoded == "" {
		return false
	}
	_, err := corework.Decode(encoded)
	return err == nil
}
