package common

var (
	BootstrapPeers = map[string][]string{
		"colosseum": {
			"/dns4/bootnode.colosseum0.quai.network/tcp/4001/p2p/12D3KooWK3nVCWjToi3igfs8oyJscVQYLd4SmQanohAuF8M6eZBn",
		},
		"garden": {
			"/dns4/bootnode.garden0.quai.network/tcp/4001/p2p/12D3KooWRQrLVEeJtfyKoJDYWYjryBKR8qxkDooMMzyf2ZpLaZRR",
			"/dns4/bootnode.garden1.quai.network/tcp/4001/p2p/12D3KooWSb49ccXFWPCsvi7rzCbqBUK2xfuRC2xbo6KnUZk3YaVg",
			"/dns4/bootnode.garden2.quai.network/tcp/4001/p2p/12D3KooWR3xMB6sCpsowQcvtdMKmKbTaiDcDFAXuWABdZVPWaVuo",
			"/dns4/bootnode.garden3.quai.network/tcp/4001/p2p/12D3KooWJnWmBukEbZtGPPJvT1r4tQ97CRSGmnjHewcrjNB8oRxU",
		},
		"orchard": {
			"/dns4/bootnode.orchard0.quai.network/tcp/4001/p2p/12D3KooWSu4bS67yLVUcFRdbrUbjQJdNeqe1cJ4pjHf77zwX5AHy",
			"/dns4/bootnode.orchard1.quai.network/tcp/4001/p2p/12D3KooWBdtyVjqXSG3zE7qEz6K1wu3Cjc9B6A9bsh9A7xRwg1nP",
			"/dns4/bootnode.orchard2.quai.network/tcp/4001/p2p/12D3KooWRdAA7rCedzbRewzw2R8zAY1QDiiA1KhDRxHXsZvukeBC",
		},
		"lighthouse": {
			"/dns4/host-go-quai/tcp/4001/p2p/12D3KooWS83uhvCfyNeAV24nEsp3DHrygDD39rZiVy6Gabv6pqxt",
		},
	}
)
