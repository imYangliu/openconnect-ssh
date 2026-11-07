module github.com/imyangliu/openconnect-ssh

go 1.25.6

require (
	github.com/mythologyli/zju-connect v0.0.0
	github.com/sagernet/sing-openconnect v0.1.1-0.20261103042440-6c98622bada6
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/net v0.55.0
	golang.zx2c4.com/wireguard v0.0.0-20260522210424-ecfc5a8d5446
)

require (
	github.com/anchore/go-lzo v0.1.0 // indirect
	github.com/boombuler/barcode v1.1.0 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/certificate-transparency-go v1.3.2 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/pierrec/lz4/v4 v4.1.21 // indirect
	github.com/pion/dtls/v3 v3.1.5 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v4 v4.0.2 // indirect
	github.com/pquerna/otp v1.5.0 // indirect
	github.com/sagernet/sing v0.8.12-0.20260702081104-2ded2af32d3d // indirect
	github.com/smallstep/pkcs7 v0.1.1 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	go4.org/intern v0.0.0-20230525184215-6c62f75575cb // indirect
	go4.org/unsafe/assume-no-moving-gc v0.0.0-20231121144256-b99613f794b6 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/exp v0.0.0-20260508232706-74f9aab9d74a // indirect
	golang.org/x/sys v0.45.0
	golang.org/x/term v0.43.0
	golang.org/x/time v0.15.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	gvisor.dev/gvisor v0.0.0-20260129214308-cb856800aa1c // indirect
	inet.af/netaddr v0.0.0-20230525184311-b8eac61e914a // indirect
)

replace github.com/mythologyli/zju-connect => ./third_party/zju-connect

replace inet.af/netaddr => github.com/inetaf/netaddr v0.0.0-20230525184311-b8eac61e914a

replace github.com/sagernet/sing-openconnect => ./third_party/sing-openconnect
