// provision-cert generates a non-exportable RSA key directly inside the
// current user's Windows CNG certificate store (Microsoft Software Key
// Storage Provider) and self-signs a certificate for it. The private key
// never exists as a file and is never exportable — it is generated in
// place by CNG and all signing happens through the OS. Only the public
// certificate (safe to share) is written to disk, for uploading to the
// Azure AD app registration as a certificate credential.
package main

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/google/certtostore"
)

const (
	provider  = "Microsoft Software Key Storage Provider"
	container = "teams-trello-sync-key"
	commonName = "teams-trello-sync-service"
	validity  = 2 * 365 * 24 * time.Hour
)

func main() {
	store, err := certtostore.OpenWinCertStoreCurrentUser(provider, container, nil, nil, false)
	if err != nil {
		log.Fatalf("open cert store: %v", err)
	}
	defer store.Close()

	signer, err := store.Generate(certtostore.GenerateOpts{Algorithm: certtostore.RSA, Size: 2048})
	if err != nil {
		log.Fatalf("generate non-exportable key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     time.Now().Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		IsCA:         false,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	if err != nil {
		log.Fatalf("self-sign certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		log.Fatalf("parse generated certificate: %v", err)
	}

	// Not calling store.Store(cert, nil) here: it links a visible certificate
	// object into the store (cosmetic, shows up in certmgr.msc) but a bug in
	// certtostore's Windows internals panics on some setups. Not needed for
	// our purposes — Key() re-derives the same non-exportable signer later
	// by (provider, container) alone, with no stored cert required.

	if err := os.WriteFile("teams-trello-sync.cer", der, 0644); err != nil {
		log.Fatalf("write .cer: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile("teams-trello-sync.pem", pemBytes, 0644); err != nil {
		log.Fatalf("write .pem: %v", err)
	}

	thumb := sha1.Sum(der)
	fmt.Printf("provisioned non-exportable key in CNG store\n")
	fmt.Printf("  provider:  %s\n", provider)
	fmt.Printf("  container: %s\n", container)
	fmt.Printf("  subject:   CN=%s\n", commonName)
	fmt.Printf("  expires:   %s\n", cert.NotAfter.Format(time.RFC3339))
	fmt.Printf("  thumbprint (sha1, hex): %x\n", thumb)
	fmt.Printf("  thumbprint (x5t, base64url): %s\n", base64.RawURLEncoding.EncodeToString(thumb[:]))
	fmt.Printf("  public cert written to: teams-trello-sync.cer (DER), teams-trello-sync.pem\n")
}
