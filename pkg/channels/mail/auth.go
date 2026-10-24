package mail

import "fmt"

// xoauth2Response is the XOAUTH2 initial client response (Google/Microsoft format).
func xoauth2Response(user, token string) []byte {
	return []byte("user=" + user + "\x01auth=Bearer " + token + "\x01\x01")
}

// xoauth2 is an IMAP SASL client for the XOAUTH2 mechanism.
type xoauth2 struct{ user, token string }

func newXOAuth2(user, token string) *xoauth2 { return &xoauth2{user, token} }

func (a *xoauth2) Start() (mech string, ir []byte, err error) {
	return "XOAUTH2", xoauth2Response(a.user, a.token), nil
}

// Next is only called when the server rejected the token and sent a JSON
// error description as a challenge.
func (a *xoauth2) Next(challenge []byte) ([]byte, error) {
	return nil, fmt.Errorf("xoauth2 rejected: %s", challenge)
}
