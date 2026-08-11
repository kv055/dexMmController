package api

import "net/http"

// lndChannelPreset is a reviewed public peer that can be selected without
// entering its connection string by hand. Keep this catalog deliberately small:
// a preset is not an endorsement of a peer's current channel policy.
type lndChannelPreset struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	NodePubkey          string `json:"node_pubkey"`
	PeerHost            string `json:"peer_host"`
	MinFundingAmountSat int64  `json:"min_funding_amount_sat"`
	MaxFundingAmountSat int64  `json:"max_funding_amount_sat,omitempty"`
	Warning             string `json:"warning,omitempty"`
	SourceURL           string `json:"source_url"`
	VerifiedAt          string `json:"verified_at"`
	Enabled             bool   `json:"enabled"`
}

// channelPresets contains only nodes for which the operator publishes a
// connection string and channel requirement. More nodes can be added after the
// same verification rather than relying on third-party graph data.
var channelPresets = []lndChannelPreset{
	{
		ID:                  "kraken",
		Name:                "Kraken",
		NodePubkey:          "02f1a8c87607f415c8f22c00593002775941dea48869ce23096af27b0cfdcc0b69",
		PeerHost:            "52.13.118.208:9735",
		MinFundingAmountSat: 1_000_000,
		Warning:             "Kraken requires a minimum channel size of 1,000,000 sats. Channel acceptance remains subject to the remote node's current policy.",
		SourceURL:           "https://support.kraken.com/articles/9829766858260-connecting-to-kraken-s-lightning-node",
		VerifiedAt:          "2026-08-11",
		Enabled:             true,
	},
}

func enabledChannelPreset(id string) (lndChannelPreset, bool) {
	for _, preset := range channelPresets {
		if preset.ID == id && preset.Enabled {
			return preset, true
		}
	}
	return lndChannelPreset{}, false
}

func (s *Server) handleLNDChannelPresets(w http.ResponseWriter, _ *http.Request) {
	presets := make([]lndChannelPreset, 0, len(channelPresets))
	for _, preset := range channelPresets {
		if preset.Enabled {
			presets = append(presets, preset)
		}
	}
	writeJSON(w, http.StatusOK, map[string][]lndChannelPreset{"presets": presets})
}
