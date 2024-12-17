package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// Bot configuration
var (
	Token    string
	RpcURL   string = "https://api.mainnet-beta.solana.com" // Default RPC URL
	client   *rpc.Client
)

// Explorer links structure
type ExplorerLink struct {
	Name string
	URL  string
	URLRegex *regexp.Regexp
}

var explorers = []ExplorerLink{
	{
		Name: "Solscan",
		URL:  "https://solscan.io/account/",
		URLRegex: regexp.MustCompile(`solscan\.io/account/([1-9A-HJ-NP-Za-km-z]{32,44})`),
	},
	{
		Name: "BullX",
		URL:  "https://bullx.io/terminal?chainId=1399811149&address=",
		URLRegex: regexp.MustCompile(`bullx\.io/terminal\?chainId=1399811149&address=([1-9A-HJ-NP-Za-km-z]{32,44})`),
	},
	{
		Name: "Photon",
		URL:  "https://photon-sol.tinyastro.io/en/lp/",
		URLRegex: regexp.MustCompile(`photon-sol\.tinyastro\.io/en/lp/([1-9A-HJ-NP-Za-km-z]{32,44})`),
	},
}

// Compile regex pattern for Solana addresses
var solanaAddressPattern = regexp.MustCompile(`[1-9A-HJ-NP-Za-km-z]{32,44}`)

func init() {
	flag.StringVar(&Token, "t", "", "Bot Token")
	flag.StringVar(&RpcURL, "rpc", "https://api.mainnet-beta.solana.com", "Solana RPC Url")
	flag.Parse()

	client = rpc.New(RpcURL)
}

func main() {
	if Token == "" {
		log.Fatal("No token provided. Please run with -t <bot token>")
	}

	// Create new Discord session
	dg, err := discordgo.New("Bot " + Token)
	if err != nil {
		log.Fatal("Error creating Discord session:", err)
	}

	// Register handlers
	dg.AddHandler(ready)
	dg.AddHandler(messageCreate)

	// Open websocket connection
	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening connection:", err)
	}

	// Wait for interrupt signal
	fmt.Println("Bot is running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM)
	<-sc

	// Close Discord session
	dg.Close()
}

func ready(s *discordgo.Session, event *discordgo.Ready) {
	s.UpdateGameStatus(0, "Watching Solana addresses")
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Process URLs first
	foundInURL := false
	for _, explorer := range explorers {
		matches := explorer.URLRegex.FindStringSubmatch(m.Content)
		if len(matches) > 1 {
			address := matches[1]
			if validateSolanaAddress(address) {
				handleAddress(s, m.ChannelID, address, m.ID)
				foundInURL = true
				break
			}
		}
	}

	// If no address was found in URLs, look for raw addresses
	if !foundInURL {
		matches := solanaAddressPattern.FindAllString(m.Content, -1)
		processed := make(map[string]bool)
		for _, address := range matches {
			if processed[address] {
				continue
			}
			processed[address] = true

			if validateSolanaAddress(address) {
				handleAddress(s, m.ChannelID, address, m.ID)
			}
		}
	}
}

func handleAddress(s *discordgo.Session, channelID string, address string, messageID string) {
	pubkey, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return
	}

	acc, err := client.GetAccountInfoWithOpts(
		context.Background(),
		pubkey,
		&rpc.GetAccountInfoOpts{
			Commitment: "confirmed",
			Encoding:  "base64",
		},
	)
	if err != nil {
		log.Printf("Error getting account info: %v", err)
		return
	}

	if acc == nil || acc.Value == nil {
		return
	}

	// Create message reference for reply
	reference := &discordgo.MessageReference{
		MessageID: messageID,
		ChannelID: channelID,
		GuildID:   "", // This will be filled automatically
	}

	if acc.Value.Executable {
		sendContractEmbed(s, channelID, address, reference)
	} else {
		if len(acc.Value.Data.GetBinary()) > 0 {
			sendContractEmbed(s, channelID, address, reference)
		} else {
			balance := float64(acc.Value.Lamports) / 1e9
			sendWalletEmbed(s, channelID, address, balance, reference)
		}
	}
}

func sendWalletEmbed(s *discordgo.Session, channelID string, address string, balance float64, reference *discordgo.MessageReference) {
	embed := &discordgo.MessageEmbed{
		Title:       "Solana Wallet",
		Description: fmt.Sprintf("Address: `%s`", address),
		Color:       0x00FF00,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Balance",
				Value:  fmt.Sprintf("%.4f SOL", balance),
				Inline: false,
			},
			{
				Name:   "View on Solscan",
				Value:  fmt.Sprintf("[Click here](https://solscan.io/account/%s)", address),
				Inline: false,
			},
		},
	}

	s.ChannelMessageSendEmbedReply(channelID, embed, reference)
}

func sendContractEmbed(s *discordgo.Session, channelID string, address string, reference *discordgo.MessageReference) {
	embed := &discordgo.MessageEmbed{
		Title:       "Solana Contract Explorer",
		Description: fmt.Sprintf("Explorer links for address: `%s`", address),
		Color:       0x1E88E5,
		Fields:      make([]*discordgo.MessageEmbedField, 0),
	}

	for _, explorer := range explorers {
		fullURL := explorer.URL + address
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   explorer.Name,
			Value:  fmt.Sprintf("[View on %s](%s)", explorer.Name, fullURL),
			Inline: false,
		})
	}

	s.ChannelMessageSendEmbedReply(channelID, embed, reference)
}

func validateSolanaAddress(address string) bool {
	if len(address) < 32 || len(address) > 44 {
		return false
	}
	
	validChars := "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	for _, char := range address {
		if !strings.ContainsRune(validChars, char) {
			return false
		}
	}
	
	return true
}