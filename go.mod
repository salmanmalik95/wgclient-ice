module ztnav2client

go 1.18

require (
	github.com/cenkalti/backoff/v4 v4.1.3
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/uuid v1.3.0
	github.com/pion/dtls/v2 v2.1.5
	github.com/pion/ice/v2 v2.2.7
	github.com/pion/logging v0.2.2
	github.com/pion/mdns v0.0.5
	github.com/pion/randutil v0.1.0
	github.com/pion/stun v0.3.5
	github.com/pion/transport v0.14.1
	github.com/pion/turn/v2 v2.0.9
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.8.1
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/crypto v0.0.0-20220513210258-46612604a0f9
	golang.org/x/net v0.2.0
	golang.org/x/sys v0.2.0
	golang.zx2c4.com/wireguard v0.0.0-20211209221555-9c9e7e272434 // indirect
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20211215182854-7a385b3431de
	golang.zx2c4.com/wireguard/windows v0.5.1 // indirect
	google.golang.org/grpc v1.43.0
	google.golang.org/protobuf v1.28.1
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
)

require (
	github.com/coreos/go-iptables v0.6.0
	github.com/gin-gonic/gin v1.8.1
	github.com/google/nftables v0.0.0-20220808154552-2eca00135732
	github.com/libp2p/go-netroute v0.2.0
	github.com/netbirdio/netbird v0.11.4
)

require (
	github.com/BurntSushi/toml v0.4.1 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/creack/pty v1.1.18 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/gliderlabs/ssh v0.3.4 // indirect
	github.com/go-playground/locales v0.14.0 // indirect
	github.com/go-playground/universal-translator v0.18.0 // indirect
	github.com/go-playground/validator/v10 v10.10.0 // indirect
	github.com/goccy/go-json v0.9.7 // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/google/gopacket v1.1.19 // indirect
	github.com/josharian/native v0.0.0-20200817173448-b6b71def0850 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/leodido/go-urn v1.2.1 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/mdlayher/genetlink v1.1.0 // indirect
	github.com/mdlayher/netlink v1.4.2 // indirect
	github.com/mdlayher/socket v0.0.0-20211102153432-57e3fa563ecb // indirect
	github.com/miekg/dns v1.1.41 // indirect
	github.com/modern-go/concurrent v0.0.0-20180228061459-e0a39a4cb421 // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.0.1 // indirect
	github.com/pion/udp v0.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/ugorji/go/codec v1.2.7 // indirect
	github.com/vishvananda/netns v0.0.0-20191106174202-0a2b9b5464df // indirect
	golang.org/x/mod v0.6.0-dev.0.20220419223038-86c51ed26bb4 // indirect
	golang.org/x/term v0.2.0 // indirect
	golang.org/x/text v0.4.0 // indirect
	golang.org/x/tools v0.1.12 // indirect
	golang.zx2c4.com/go118/netip v0.0.0-20211111135330-a4a02eeacf9d // indirect
	golang.zx2c4.com/wintun v0.0.0-20211104114900-415007cec224 // indirect
	google.golang.org/genproto v0.0.0-20211208223120-3a66f561d7aa // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	honnef.co/go/tools v0.2.2 // indirect
)

replace github.com/kardianos/service => github.com/netbirdio/service v0.0.0-20220905002524-6ac14ad5ea84

replace github.com/getlantern/systray => github.com/netbirdio/systray v0.0.0-20221012095658-dc8eda872c0c
