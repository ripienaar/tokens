// Copyright (c) 2021, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package tokens

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	iu "github.com/choria-io/go-choria/internal/util"
	"github.com/golang-jwt/jwt/v4"
)

type StandardClaims struct {
	// Purpose indicates the type of JWT for type discovery
	Purpose Purpose `json:"purpose"`

	// TrustChainSignature is a structure that helps to verify a chain of trust to a org issuer
	TrustChainSignature string `json:"tcs,omitempty"`

	// PublicKey is a ED25519 public key associated with this token
	PublicKey string `json:"public_key,omitempty"`

	// IssuerExpiresAt is the expiry time of the issuer, if set will be checked in addition to the expiry time of the token itself
	IssuerExpiresAt *jwt.NumericDate `json:"issexp,omitempty"`

	jwt.RegisteredClaims
}

func (c *StandardClaims) verifyIssuerExpiry(req bool) bool {
	// org issuer tokens has a tcs but the org issuer has no expiry time so we can skip
	if !strings.HasPrefix(c.Issuer, ChainIssuerPrefix) {
		return !req
	}

	// without a tcs this isnt a chained token so there's no point in validating
	if c.TrustChainSignature == "" {
		return !req
	}

	if c.IssuerExpiresAt == nil {
		return !req
	}

	return c.IssuerExpiresAt.After(time.Now())
}

// IsChainedIssuer determines if this is a token capable of issuing users as part of a chain
// without verify being true one can not be 100% certain it's valid to do that but its a strong hint
func (c *StandardClaims) IsChainedIssuer(verify bool) bool {
	if len(c.TrustChainSignature) == 0 {
		return false
	}

	if !strings.HasPrefix(c.Issuer, OrgIssuerPrefix) {
		return false
	}

	if !verify {
		return true
	}

	dat, err := c.OrgIssuerChainData()
	if err != nil {
		return false
	}

	pubK, err := hex.DecodeString(strings.TrimPrefix(c.Issuer, OrgIssuerPrefix))
	if err != nil {
		return false
	}

	sig, err := hex.DecodeString(c.TrustChainSignature)
	if err != nil {
		return false
	}

	ok, _ := iu.Ed24419Verify(pubK, dat, sig)

	return ok
}

// OrgIssuerChainData creates data that the org issuer would sign and embed in the token as TrustChainSignature
func (c *StandardClaims) OrgIssuerChainData() ([]byte, error) {
	if c.ID == "" {
		return nil, fmt.Errorf("no token id set")
	}
	if c.PublicKey == "" {
		return nil, fmt.Errorf("no public key set")
	}

	return []byte(fmt.Sprintf("%s.%s", c.ID, c.PublicKey)), nil
}

// SetOrgIssuer sets the issuer field for users issued by the Org Issuer
func (c *StandardClaims) SetOrgIssuer(pk ed25519.PublicKey) {
	c.Issuer = fmt.Sprintf("%s%s", OrgIssuerPrefix, hex.EncodeToString(pk))
}

// SetChainIssuer used by Login Handlers that create users in a chain to set an appropriate issuer on created users
func (c *StandardClaims) SetChainIssuer(ci *ClientIDClaims) error {
	if ci.ID == "" {
		return fmt.Errorf("id not set")
	}
	if ci.PublicKey == "" {
		return fmt.Errorf("public key not set")
	}

	c.Issuer = fmt.Sprintf("%s%s.%s", ChainIssuerPrefix, ci.ID, ci.PublicKey)
	c.IssuerExpiresAt = ci.ExpiresAt

	return nil
}

// ChainIssuerData is the data that should be signed on a user to create a chain of trust between Org Issuer, Client Login Handler and Client.
//
// The Issuer should already be set using SetChainIssuer()
func (c *StandardClaims) ChainIssuerData(chainSig string) ([]byte, error) {
	if c.ID == "" {
		return nil, fmt.Errorf("id not set")
	}
	if c.Issuer == "" {
		return nil, fmt.Errorf("issuer not set")
	}
	if !strings.HasPrefix(c.Issuer, ChainIssuerPrefix) {
		return nil, fmt.Errorf("invalid issuer prefix")
	}

	parts := strings.Split(strings.TrimPrefix(c.Issuer, ChainIssuerPrefix), ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid issuer data")
	}

	return []byte(fmt.Sprintf("%s.%s", c.ID, chainSig)), nil
}

// SetChainUserTrustSignature sets the TrustChainSignature for a user issued by a ChainIssuer like AAA Login Server
func (c *StandardClaims) SetChainUserTrustSignature(h *ClientIDClaims, sig []byte) {
	c.TrustChainSignature = fmt.Sprintf("%s.%s", h.TrustChainSignature, hex.EncodeToString(sig))
}

// SetChainIssuerTrustSignature sets the TrustChainSignature for a user who may issue others like a AAA Login Server
func (c *StandardClaims) SetChainIssuerTrustSignature(sig []byte) {
	c.TrustChainSignature = hex.EncodeToString(sig)
}

// IsSignedByIssuer uses the chain data in Issuer and TrustChainSignature to determine if an issuer signed a token
func (c *StandardClaims) IsSignedByIssuer(pk ed25519.PublicKey) (bool, error) {
	if c.Issuer == "" {
		return false, fmt.Errorf("no issuer set")
	}
	if c.PublicKey == "" {
		return false, fmt.Errorf("no public key set")
	}
	if c.TrustChainSignature == "" {
		return false, fmt.Errorf("no trust chain signature set")
	}
	if c.ID == "" {
		return false, fmt.Errorf("id not set")
	}

	switch {
	case strings.HasPrefix(c.Issuer, OrgIssuerPrefix):
		// This would be a token that is allowed to create clients in a chain.
		//
		// Its Issuer is set to I-issuerPubk
		// Its chain sig is signed by the issuer "<id>.<pubk>" of this token, obtained from OrgIssuerChainData()
		//
		// So we simply check if the signature in the TrustChainSignature match the data if signed by the
		// supplied issuer public key

		if c.Issuer != fmt.Sprintf("%s%s", OrgIssuerPrefix, hex.EncodeToString(pk)) {
			return false, fmt.Errorf("public keys do not match")
		}

		sig, err := hex.DecodeString(c.TrustChainSignature)
		if err != nil {
			return false, fmt.Errorf("invalid trust chain signature: %w", err)
		}

		dat, err := c.OrgIssuerChainData()
		if err != nil {
			return false, err
		}

		return iu.Ed24419Verify(pk, dat, sig)

	case strings.HasPrefix(c.Issuer, ChainIssuerPrefix):
		// This is a token that was created by one in the chain - not the org issuer.
		//
		// Its Issuer is set to C-<creator id>.<creator pubk>
		// Its chain sig is set to <creator tcs>.hex(sign(creatorId,<creator tcs>))
		//
		// We know what the content of tcs unsigned is from the Issuer field
		// and we are given the issuer public key, so we can confirm the issuer
		// we are interested in signed the tcs.
		//
		// We know the holder of the creator private key made it because we have
		// it's public key and can confirm that, we know its the public key of
		// the creator since its in the tcs set there by our trusted issuer.
		//
		// We can confirm the tcs is valid and matches whats in the sig made by
		// the creator because we verify it using the requested issuer pubk

		// get details of the login handler or provisioner
		issuerChainData := strings.TrimPrefix(c.Issuer, ChainIssuerPrefix)

		parts := strings.Split(issuerChainData, ".")
		if len(parts) != 2 {
			return false, fmt.Errorf("invalid issuer content")
		}

		if len(parts[0]) == 0 {
			return false, fmt.Errorf("invalid id in issuer")
		}
		if len(parts[1]) == 0 {
			return false, fmt.Errorf("invalid public key in issuer")
		}

		hPubk, err := hex.DecodeString(parts[1])
		if err != nil {
			return false, fmt.Errorf("invalid public key in issuer data")
		}

		// now we check the signature is data + "." + sig(id+ "." + data)
		parts = strings.Split(c.TrustChainSignature, ".")
		if len(parts) != 2 {
			return false, fmt.Errorf("invalid trust chain signature")
		}
		if len(parts[0]) == 0 || len(parts[1]) == 0 {
			return false, fmt.Errorf("invalid trust chain signature")
		}

		sig, err := hex.DecodeString(parts[1])
		if err != nil {
			return false, fmt.Errorf("invalid signature in chain signature: %w", err)
		}

		// this is the signature from the handler
		ok, err := iu.Ed24419Verify(hPubk, []byte(fmt.Sprintf("%s.%s", c.ID, parts[0])), sig)
		if err != nil {
			return false, fmt.Errorf("chain signature validation failed: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("invalid chain signature")
		}

		return true, nil

	default:
		return false, fmt.Errorf("unsupported issuer format")
	}
}
