package dutycal

import (
	"net/http"
	"net/url"

	"ancient-solutions.com/ancientauth"
)

type AuthDetails struct {
	User     string
	LoginUrl url.URL
}

type authManager struct {
	auth *ancientauth.Authenticator
}

/*
Generate a new authentication manager object. Basically just wraps the
AncientAuth authenticator functions.
*/
func NewAuthManager(auth *ancientauth.Authenticator) *authManager {
	return &authManager{auth: auth}
}

/*
Extract any authentication information from the HTTP request and,
if appropriate, generate a login link.
*/
func (a *authManager) GenAuthDetails(req *http.Request, ad *AuthDetails) error {
	var err error

	ad.User = a.auth.GetAuthenticatedUser(req)
	if len(ad.User) == 0 {
		ad.LoginUrl, err = a.auth.MakeAuthorizationURL(req)
	}

	return err
}
