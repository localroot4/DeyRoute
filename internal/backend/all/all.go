// Package all registers every tunnel backend. Import it for side effects:
//
//	import _ "github.com/localroot4/deyroute/internal/backend/all"
package all

import (
	// Each backend registers itself in init().
	_ "github.com/localroot4/deyroute/internal/backend/backhaul"
	_ "github.com/localroot4/deyroute/internal/backend/chisel"
	_ "github.com/localroot4/deyroute/internal/backend/direct"
	_ "github.com/localroot4/deyroute/internal/backend/frp"
	_ "github.com/localroot4/deyroute/internal/backend/gost"
	_ "github.com/localroot4/deyroute/internal/backend/hysteria2"
	_ "github.com/localroot4/deyroute/internal/backend/rathole"
	_ "github.com/localroot4/deyroute/internal/backend/waterwall"
	_ "github.com/localroot4/deyroute/internal/backend/wireguard"
	_ "github.com/localroot4/deyroute/internal/backend/xray"
)
