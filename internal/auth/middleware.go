package auth

import "net/http"

// StripUntrustedIdentity removes the forward-auth identity header from any
// request whose direct peer is not a trusted proxy. This is defense-in-depth:
// even though ForwardAuth re-checks trust, stripping ensures a spoofed header
// can never be observed by downstream handlers or logs.
func (a *Authenticator) StripUntrustedIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, ok := peerAddr(r)
		if !ok || !a.cfg.TrustsAddr(peer) {
			r.Header.Del(a.cfg.ForwardAuthHeader)
		}
		next.ServeHTTP(w, r)
	})
}
