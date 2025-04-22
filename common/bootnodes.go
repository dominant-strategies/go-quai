package common

var (
	BootstrapPeers = map[string][]string{
		"colosseum": {
			"/ip4/35.188.147.227/tcp/4002/p2p/12D3KooWB72Vs7gfLgvJsmR7cex7tG5WY7WDdqJD124Res3byDyk",
			"/ip4/34.23.115.164/tcp/4002/p2p/12D3KooWPdEr7wZfcWg9d2EoX6cmnyx3GvEnV5pVJvRxMUbkKesB",
			"/ip4/34.145.104.213/tcp/4002/p2p/12D3KooWMHTexdSq1zQ2Sn4L8zddEa8PWK8TGM56ZHszRuoUPoq1",
		},
		"garden": {
			"/dns4/bootnode.garden0.quai.network/tcp/4002/p2p/12D3KooWRQrLVEeJtfyKoJDYWYjryBKR8qxkDooMMzyf2ZpLaZRR",
			"/dns4/bootnode.garden1.quai.network/tcp/4002/p2p/12D3KooWLzhZXUdqhwbGpezddPkpGtZ6v7obzPkWVkfY1s6ZsX6S",
			"/dns4/bootnode.garden2.quai.network/tcp/4002/p2p/12D3KooWR3xMB6sCpsowQcvtdMKmKbTaiDcDFAXuWABdZVPWaVuo",
			"/dns4/bootnode.garden3.quai.network/tcp/4002/p2p/12D3KooWJnWmBukEbZtGPPJvT1r4tQ97CRSGmnjHewcrjNB8oRxU",
		},
		"orchard": {
			"/ip4/35.196.106.212/tcp/4002/p2p/12D3KooWK755v2ynqqgvrEeTQe38E1i556jDwX9JcduotE1bfvjU",
			"/ip4/34.136.242.207/tcp/4002/p2p/12D3KooWMjRbhyjQ5PCbt6qzMcVJx3n5NRx6uUv43yQP9ec3DV3U",
			"/ip4/35.247.60.58/tcp/4002/p2p/12D3KooWS3GnpJMNqKSSP8eCGYw53xtwdRFVZ5jj1RsCnFX2R1eu",
		},
		"lighthouse": {
			"/dns4/host-go-quai/tcp/4002/p2p/12D3KooWS83uhvCfyNeAV24nEsp3DHrygDD39rZiVy6Gabv6pqxt",
		},
	}
)
