// Command minisign is a tiny release helper (keygen, sign, verify) built on
// aead.dev/minisign so CI does not need the minisign tool.
//
//	go run ./scripts/minisign keygen -pub minisign.pub -sec minisign.key
//	MINISIGN_SECRET_KEY="$(cat minisign.key)" go run ./scripts/minisign sign -in SHA256SUMS
//	go run ./scripts/minisign verify -in SHA256SUMS -pubkey RWT...
//
// The secret key is read from $MINISIGN_SECRET_KEY (unencrypted key text, or
// an encrypted key with $MINISIGN_PASSWORD). It is never written to disk by
// the sign command.
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"aead.dev/minisign"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "minisign:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: minisign keygen|sign|verify [flags]")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	pubPath := fs.String("pub", "minisign.pub", "public key output")
	secPath := fs.String("sec", "minisign.key", "secret key output (0600)")
	_ = fs.Parse(args)
	pub, sec, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	secText, err := sec.MarshalText()
	if err != nil {
		return err
	}
	pubText, err := pub.MarshalText()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*secPath, append(secText, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(*pubPath, append(pubText, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Println(pub.String())
	return nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	in := fs.String("in", "", "file to sign")
	out := fs.String("out", "", "signature output (default <in>.minisig)")
	_ = fs.Parse(args)
	if *in == "" {
		return fmt.Errorf("-in is required")
	}
	if *out == "" {
		*out = *in + ".minisig"
	}
	keyText := strings.TrimSpace(os.Getenv("MINISIGN_SECRET_KEY"))
	if keyText == "" {
		return fmt.Errorf("MINISIGN_SECRET_KEY is empty")
	}
	var key minisign.PrivateKey
	if pw := os.Getenv("MINISIGN_PASSWORD"); pw != "" {
		k, err := minisign.DecryptKey(pw, []byte(keyText))
		if err != nil {
			return err
		}
		key = k
	} else if err := key.UnmarshalText([]byte(keyText)); err != nil {
		return err
	}
	msg, err := os.ReadFile(*in) // #nosec G304 -- release file chosen by the caller
	if err != nil {
		return err
	}
	name := (*in)[strings.LastIndex(*in, "/")+1:]
	trusted := fmt.Sprintf("timestamp:%d\tfile:%s", time.Now().Unix(), name)
	sig := minisign.SignWithComments(key, msg, trusted, "signature from DEYROUTE release key")
	return os.WriteFile(*out, sig, 0o644) // #nosec G306 -- signature is public
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	in := fs.String("in", "", "signed file")
	sigPath := fs.String("sig", "", "signature (default <in>.minisig)")
	pubKey := fs.String("pubkey", "", "public key (RW...)")
	_ = fs.Parse(args)
	if *sigPath == "" {
		*sigPath = *in + ".minisig"
	}
	var pub minisign.PublicKey
	if err := pub.UnmarshalText([]byte(*pubKey)); err != nil {
		return err
	}
	msg, err := os.ReadFile(*in) // #nosec G304 -- caller-chosen file
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(*sigPath) // #nosec G304 -- caller-chosen file
	if err != nil {
		return err
	}
	if !minisign.Verify(pub, msg, sig) {
		return fmt.Errorf("signature INVALID")
	}
	fmt.Println("signature OK")
	return nil
}
