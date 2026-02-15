package auth

import (
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/estara-ai/www/internal/db/queries"
)

// webauthnUser adapts our database User + credentials to the webauthn.User interface.
type webauthnUser struct {
	id          string
	email       string
	displayName string
	credentials []webauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte {
	return []byte(u.id)
}

func (u *webauthnUser) WebAuthnName() string {
	return u.email
}

func (u *webauthnUser) WebAuthnDisplayName() string {
	return u.displayName
}

func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

// toWebAuthnUser converts a DB user + stored credentials into a webauthn.User.
func toWebAuthnUser(user *User, creds []queries.WebauthnCredential) *webauthnUser {
	displayName := user.Email
	if user.FirstName != nil && *user.FirstName != "" {
		displayName = *user.FirstName
		if user.LastName != nil && *user.LastName != "" {
			displayName += " " + *user.LastName
		}
	}

	wCreds := make([]webauthn.Credential, 0, len(creds))
	for _, c := range creds {
		wCreds = append(wCreds, dbCredToWebAuthn(c))
	}

	return &webauthnUser{
		id:          user.ID,
		email:       user.Email,
		displayName: displayName,
		credentials: wCreds,
	}
}

// dbCredToWebAuthn converts a database credential row into a webauthn.Credential.
func dbCredToWebAuthn(c queries.WebauthnCredential) webauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, 0, len(c.Transport))
	for _, t := range c.Transport {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}

	return webauthn.Credential{
		ID:              c.CredentialID,
		PublicKey:       c.PublicKey,
		AttestationType: c.AttestationType,
		Transport:       transports,
		Flags:           webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(c.FlagsValue)),
		Authenticator: webauthn.Authenticator{
			AAGUID:       c.Aaguid,
			SignCount:    uint32(c.SignCount),
			CloneWarning: c.CloneWarning,
		},
	}
}
