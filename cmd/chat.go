package cmd

import (
	"context"
	"os"

	"github.com/dominant-strategies/go-quai/log"
	p2pnode "github.com/dominant-strategies/go-quai/p2p/node"
	"github.com/dominant-strategies/go-quai/test/chatroom"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// chatCmd represents the chat command
var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Joins a chat room to chat with other peers",
	Long: `Joins a chat room to chat with other peers.
The chat room is identified by the topic name.
The chat room UI opens in the command line as a TUI (terminal user interface).
To quit the chat, press Ctrl+C.`,
	Run:                        runChat,
	SilenceUsage:               true,
	SuggestionsMinimumDistance: 2,
	Args:                       cobra.RangeArgs(0, 2),
	Example:                    `go-quai chat --nickname=messi --privkey=private.key`,
}

func init() {
	rootCmd.AddCommand(chatCmd)
	// Configure flag for chat room name to join (i.e. topic)
	chatCmd.Flags().StringP("room", "r", "quai", "chat room to join")
	viper.BindPFlag("room", chatCmd.Flags().Lookup("room"))
	// Configure flag for nickname to use in chat
	chatCmd.Flags().StringP("nickname", "n", "anonymous", "nickname to use in chat")
	viper.BindPFlag("nickname", chatCmd.Flags().Lookup("nickname"))
}

func runChat(cmd *cobra.Command, args []string) {
	log.Infof("Starting chat app")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	room := viper.GetString("room")
	nickname := viper.GetString("nickname")
	log.Infof("Joining chat room %s with nickname %s", room, nickname)

	ipaddr := "0.0.0.0"
	port := "0"
	privKeyFile := viper.GetString("privkey")
	node, err := p2pnode.NewNode(ctx, ipaddr, port, privKeyFile)
	if err != nil {
		log.Fatalf("error creating node: %s", err)
	}

	// log the p2p node's ID
	log.Infof("node created: %s", node.ID().Pretty())
	// log the p2p node's listening addresses
	for _, addr := range node.Addrs() {
		log.Infof("listening on: %s", addr)
	}
	// initialize the DHT
	if err := node.InitializeDHT(); err != nil {
		log.Fatalf("error initializing DHT: %s", err)
		os.Exit(1)
	}

	// if the node is not a bootstrap server, bootstrap the DHT
	if !viper.GetBool("server") {
		log.Infof("bootstrapping DHT...")
		if err := node.BootstrapDHT(); err != nil {
			log.Fatalf("error bootstrapping DHT: %s", err)
			os.Exit(1)
		}
	} else {
		log.Infof("starting node as bootstrap server")
	}
	// initialize mDNS discovery
	if err := node.InitializeMDNS(); err != nil {
		log.Fatalf("error initializing mDNS discovery: %s", err)
		os.Exit(1)
	}
	log.Infof("mDNS discovery initialized")

	// join the chat room
	cr, err := chatroom.JoinChatRoom(ctx, node, node.ID(), nickname, room)
	if err != nil {
		panic(err)
	}

	// draw the UI
	ui := chatroom.NewChatUI(cr)
	if err = ui.Run(); err != nil {
		log.Errorf("error running text UI: %s", err)
	}
}
