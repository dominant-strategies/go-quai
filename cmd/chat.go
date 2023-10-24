package cmd

import (
	"context"

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
	chatCmd.MarkFlagRequired("nickname")
}

func runChat(cmd *cobra.Command, args []string) {
	log.Infof("Starting chat app")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	room := viper.GetString("room")
	nickname := viper.GetString("nickname")
	log.Infof("Joining chat room %s with nickname %s", room, nickname)

	node, err := p2pnode.NewNode(ctx)
	if err != nil {
		log.Fatalf("error creating node: %s", err)
	}

	// Start the node
	if err := node.Start(); err != nil {
		log.Fatalf("error starting node: %s", err)
	}

	// join the chat room
	cr, err := chatroom.JoinChatRoom(ctx, node, node.ID(), nickname, room)
	if err != nil {
		panic(err)
	}

	// draw the UI
	ui := chatroom.NewChatUI(cr)
	// set logger to null logger (only log to file)
	log.ConfigureLogger(log.WithOutput(log.ToNull()))
	if err = ui.Run(); err != nil {
		log.Errorf("error running text UI: %s", err)
	}
}
