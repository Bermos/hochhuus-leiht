// Package auth keeps the list to the residents who know the house code.
//
// There are no accounts. Whoever enters the house code gets a session cookie;
// whoever enters the admin code gets one that may also change every listing.
// A second, long-lived cookie identifies the browser, so a resident can edit
// the listings they created from it.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookie = "hh_session"
	ownerCookie   = "hh_owner"
	sessionTTL    = 180 * 24 * time.Hour
	ownerTTL      = 10 * 365 * 24 * time.Hour
)

// Role is what a request may do.
type Role int

const (
	// Guest has not entered the house code.
	Guest Role = iota
	// Member may read the list and change their own listings.
	Member
	// Admin may change every listing and import backups.
	Admin
)

// Auth checks codes and issues cookies.
type Auth struct {
	houseCode     string
	adminCode     string
	ownerKey      []byte
	sessionKey    []byte
	secureCookies bool
}

// New builds an Auth. secret signs the cookies; an empty houseCode leaves
// the list open to anyone, which is meant for local development and previews.
func New(secret, houseCode, adminCode string, secureCookies bool) *Auth {
	mac := func(label string) []byte {
		h := hmac.New(sha256.New, []byte(secret))
		h.Write([]byte(label))
		return h.Sum(nil)
	}
	return &Auth{
		houseCode: houseCode,
		adminCode: adminCode,
		ownerKey:  mac("owner"),
		// Changing either code signs everybody out.
		sessionKey:    mac("session\x00" + houseCode + "\x00" + adminCode),
		secureCookies: secureCookies,
	}
}

// Open reports whether the list needs no code.
func (a *Auth) Open() bool { return a.houseCode == "" }

// Role answers what the request may do.
func (a *Auth) Role(r *http.Request) Role {
	role := Guest
	if a.Open() {
		role = Member
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if v, ok := a.verify(a.sessionKey, c.Value); ok {
			parts := strings.SplitN(v, "|", 2)
			exp, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
			if time.Now().Unix() < exp {
				if parts[0] == "admin" {
					return Admin
				}
				role = Member
			}
		}
	}
	return role
}

// Login checks code and, when it is right, sets the session cookie.
func (a *Auth) Login(w http.ResponseWriter, code string) (Role, bool) {
	var role Role
	switch {
	case a.adminCode != "" && equal(code, a.adminCode):
		role = Admin
	case a.houseCode != "" && equal(code, a.houseCode):
		role = Member
	default:
		return Guest, false
	}
	name := "member"
	if role == Admin {
		name = "admin"
	}
	exp := time.Now().Add(sessionTTL)
	a.set(w, sessionCookie, a.sign(a.sessionKey, name+"|"+strconv.FormatInt(exp.Unix(), 10)), exp)
	return role, true
}

// Logout clears the session cookie. The owner cookie stays, so signing in
// again gives back the same listings.
func (a *Auth) Logout(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
}

// Owner answers the id of this browser, issuing one if it has none.
func (a *Auth) Owner(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(ownerCookie); err == nil {
		if v, ok := a.verify(a.ownerKey, c.Value); ok {
			return v
		}
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)
	a.set(w, ownerCookie, a.sign(a.ownerKey, id), time.Now().Add(ownerTTL))
	return id
}

// PeekOwner answers the id of this browser without issuing one.
func (a *Auth) PeekOwner(r *http.Request) string {
	if c, err := r.Cookie(ownerCookie); err == nil {
		if v, ok := a.verify(a.ownerKey, c.Value); ok {
			return v
		}
	}
	return ""
}

func (a *Auth) set(w http.ResponseWriter, name, value string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Expires: exp,
		HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
}

func (a *Auth) sign(key []byte, v string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(v))
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(v)) + "." + enc.EncodeToString(h.Sum(nil))
}

func (a *Auth) verify(key []byte, s string) (string, bool) {
	payload, sig, ok := strings.Cut(s, ".")
	if !ok {
		return "", false
	}
	enc := base64.RawURLEncoding
	v, err := enc.DecodeString(payload)
	if err != nil {
		return "", false
	}
	got, err := enc.DecodeString(sig)
	if err != nil {
		return "", false
	}
	h := hmac.New(sha256.New, key)
	h.Write(v)
	if !hmac.Equal(got, h.Sum(nil)) {
		return "", false
	}
	return string(v), true
}

func equal(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(strings.TrimSpace(a))), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
