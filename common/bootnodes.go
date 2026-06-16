package common

var (
	BootstrapPeers = map[string][]string{
		"colosseum": {
			"/dns4/colosseum1.qu.ai/tcp/4002/p2p/12D3KooWB72Vs7gfLgvJsmR7cex7tG5WY7WDdqJD124Res3byDyk",
			"/dns4/colosseum2.qu.ai/tcp/4002/p2p/12D3KooWPdEr7wZfcWg9d2EoX6cmnyx3GvEnV5pVJvRxMUbkKesB",
			"/dns4/colosseum3.qu.ai/tcp/4002/p2p/12D3KooWMHTexdSq1zQ2Sn4L8zddEa8PWK8TGM56ZHszRuoUPoq1",
		},
		"garden": {},
		"orchard": {
			"/dns4/orchard1.qu.ai/tcp/4002/p2p/12D3KooWK755v2ynqqgvrEeTQe38E1i556jDwX9JcduotE1bfvjU",
			"/dns4/orchard2.qu.ai/tcp/4002/p2p/12D3KooWMjRbhyjQ5PCbt6qzMcVJx3n5NRx6uUv43yQP9ec3DV3U",
		},
		"lighthouse": {},
	}
)
