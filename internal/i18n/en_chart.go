package i18n

// Chart units: byte counts and bit rates (tui/chart.go). KiB, MiB and GiB
// reuse the keys of en.go so FormatBytes matches the other size texts.
const (
	TUIBytes    Key = "tui.common.bytes"
	TUITiB      Key = "tui.common.tib"
	TUIRateBit  Key = "tui.chart.rate_bit"
	TUIRateKbit Key = "tui.chart.rate_kbit"
	TUIRateMbit Key = "tui.chart.rate_mbit"
	TUIRateGbit Key = "tui.chart.rate_gbit"
	TUIRateTbit Key = "tui.chart.rate_tbit"
)

var chartEN = map[Key]string{
	TUIBytes:    "%d B",
	TUITiB:      "%.1f TiB",
	TUIRateBit:  "%s b/s",
	TUIRateKbit: "%s kb/s",
	TUIRateMbit: "%s Mb/s",
	TUIRateGbit: "%s Gb/s",
	TUIRateTbit: "%s Tb/s",
}

// chart — merge the strings above into the English table.
func init() {
	for k, v := range chartEN {
		en[k] = v
	}
}
