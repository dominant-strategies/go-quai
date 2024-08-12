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
			"/dns4/bootnode.orchard0.quai.network/tcp/4001/p2p/12D3KooWBv5C4tSS72nBdG6Q12s7vSHYHtFcquBxAKDkfrrzseUz",
			"/dns4/bootnode.orchard1.quai.network/tcp/4001/p2p/12D3KooWBAkaxYwJUenjVQPyvtZx6XWjtosyVzBRJxM7wthbWRE5",
			"/dns4/bootnode.orchard2.quai.network/tcp/4001/p2p/12D3KooWNN1TqsrEEmitkk1LefwLNgut621sSCdncPoyMVoYT1v4",
		},
		"lighthouse": {
			// "/dns4/host-go-quai/tcp/4001/p2p/12D3KooWS83uhvCfyNeAV24nEsp3DHrygDD39rZiVy6Gabv6pqxt",
			"/ip4/35.231.60.250/tcp/4001/p2p/12D3KooWCrPfg79QwAtBBBfoL7kQzrwavEomZiAHzgdM49aEYiVr",
			"/ip4/34.138.83.236/tcp/4001/p2p/12D3KooWQL3qG1d5n7Nvcnx2FTo2b3rhEFKYSCRAvEB25pWBpL9v",
		},
	}
)
