package main

import (
	"fmt"
	"os"

	"github.com/zeus-kim/vssh/internal/server"
)

// cmdKeygen inspects or rotates this host's Ed25519 identity (~/.vssh/vssh_id).
func cmdKeygen(args []string) {
	rotate := false
	for _, a := range args {
		switch a {
		case "--rotate", "-rotate", "rotate":
			rotate = true
		}
	}
	if !rotate {
		_, pub := server.LoadOrCreateIdentity()
		fmt.Printf("identity: %s\n", server.IdentityKeyPath())
		fmt.Printf("pubkey:   %s\n", pub)
		fmt.Println("Run 'vssh keygen --rotate' to generate a NEW identity (old one is backed up).")
		return
	}
	pub, backup, err := server.RotateIdentity()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vssh: identity rotation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("rotated identity: %s\n", server.IdentityKeyPath())
	if backup != "" {
		fmt.Printf("old key backed up: %s\n", backup)
	}
	fmt.Printf("NEW pubkey: %s\n", pub)
	fmt.Print(`
The OLD key is now invalid for this host. Next steps:
  1. Restart the daemon so it serves the new identity:
       linux:  sudo systemctl restart vsshd
       darwin: launchctl bootout gui/$(id -u)/<label> && launchctl bootstrap gui/$(id -u) <plist>
  2. Re-pin this node on controllers (refresh node_keys):
       scripts/build_node_registry.sh
  3. If this host authenticates TO others as an operator, publish the NEW pubkey
     then retire the old one across the fleet:
       scripts/rotate_authorized_key.sh add "` + pub + `"
       # verify connectivity, then:
       scripts/rotate_authorized_key.sh remove "<OLD_PUBKEY>"
`)
}
