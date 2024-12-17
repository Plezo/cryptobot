package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
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

type TokenHolder struct {
	Address string
	Amount  uint64
	Percent float64
}

type TokenAnalysis struct {
	TotalSupply     uint64
	Decimals        uint8
	HolderCount     int
	TopHolders      []TokenHolder
	InsiderPercent  float64
	BundlingScore   float64
	SuspiciousFlags []string
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
	s.UpdateGameStatus(0, "sol stuff")
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

func handleAddress(s *discordgo.Session, channelID, address, messageID string) {
	pubkey, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return
	}

	acc, err := client.GetAccountInfo(context.Background(), pubkey)
	if err != nil {
		log.Printf("Error getting account info: %v", err)
		return
	}

	reference := &discordgo.MessageReference{
		MessageID: messageID,
		ChannelID: channelID,
	}

	// Check if it's a token
	if acc != nil && acc.Value != nil && len(acc.Value.Data.GetBinary()) > 0 {
		// Try to analyze as a token
		if analysis, err := analyzeToken(address); err == nil {
			sendTokenAnalysisEmbed(s, channelID, address, analysis, reference)
			return
		}
	}

	// Fall back to regular contract/wallet handling if not a token
	if acc.Value.Executable {
		sendContractEmbed(s, channelID, address, reference)
	} else {
		balance := float64(acc.Value.Lamports) / 1e9
		sendWalletEmbed(s, channelID, address, balance, reference)
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

func analyzeToken(address string) (*TokenAnalysis, error) {
	pubkey, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return nil, err
	}

	// Get token mint info
	mintAcc, err := client.GetAccountInfo(
		context.Background(),
		pubkey,
	)
	if err != nil {
		return nil, err
	}

	// Parse mint account data to get decimals
	var decimals uint8
	if mintAcc != nil && mintAcc.Value != nil {
		mintData := mintAcc.Value.Data.GetBinary()
		if len(mintData) >= 44 { // Minimum size for a mint account
			decimals = mintData[44] // Decimals is stored at offset 44
		}
	}

	// Get all token accounts with commitment type
	accounts, err := client.GetTokenLargestAccounts(
		context.Background(),
		pubkey,
		rpc.CommitmentFinalized,
	)
	if err != nil {
		log.Printf("Error getting token accounts: %v", err)
		return nil, err
	}

	var holders []TokenHolder
	totalSupply := uint64(0)

	// Process holder information
	for _, acc := range accounts.Value {
		amount, err := strconv.ParseUint(acc.Amount, 10, 64)
		if err != nil {
			log.Printf("Error parsing amount for holder %s: %v", acc.Address.String(), err)
			continue
		}

		totalSupply += amount

		holder := TokenHolder{
			Address: acc.Address.String(),
			Amount:  amount,
		}
		holders = append(holders, holder)
	}

	// Calculate percentages
	for i := range holders {
		holders[i].Percent = float64(holders[i].Amount) / float64(totalSupply) * 100
	}

	sort.Slice(holders, func(i, j int) bool {
		return holders[i].Amount > holders[j].Amount
	})

	// Calculate insider ownership (top 5 holders)
	insiderPercent := 0.0
	for i := 0; i < len(holders) && i < 5; i++ {
		insiderPercent += holders[i].Percent
	}

	analysis := &TokenAnalysis{
		TotalSupply:    totalSupply,
		Decimals:       decimals,
		HolderCount:    len(holders),
		TopHolders:     holders[:min(5, len(holders))],
		InsiderPercent: insiderPercent,
		BundlingScore:  calculateBundlingScore(holders),
	}

	if insiderPercent > 50 {
		analysis.SuspiciousFlags = append(analysis.SuspiciousFlags, "High insider ownership")
	}
	if analysis.BundlingScore > 0.7 {
		analysis.SuspiciousFlags = append(analysis.SuspiciousFlags, "Possible bundling detected")
	}

	return analysis, nil
}

func formatTokenAmount(amount uint64, decimals uint8) string {
	if decimals == 0 {
		return formatNumber(amount)
	}

	// Convert to decimal string
	full := fmt.Sprintf("%d", amount)
	if int(decimals) >= len(full) {
		// Add leading zeros if necessary
		full = strings.Repeat("0", int(decimals)-len(full)+1) + full
	}
	
	decimalPoint := len(full) - int(decimals)
	result := full[:decimalPoint] + "." + full[decimalPoint:]
	
	// Trim trailing zeros after decimal point
	result = strings.TrimRight(strings.TrimRight(result, "0"), ".")
	
	// Add commas to the whole number part
	parts := strings.Split(result, ".")
	parts[0] = addCommas(parts[0])
	
	if len(parts) > 1 {
		return parts[0] + "." + parts[1]
	}
	return parts[0]
}

func addCommas(s string) string {
	start := len(s) % 3
	if start == 0 {
		start = 3
	}
	result := s[:start]
	for i := start; i < len(s); i += 3 {
		if len(result) > 0 {
			result += ","
		}
		result += s[i:i+3]
	}
	return result
}


func calculateBundlingScore(holders []TokenHolder) float64 {
	if len(holders) < 2 {
		return 0
	}

	// Look for suspicious patterns in holder distribution
	// 1. Similar-sized holdings
	// 2. Regular distribution patterns
	// 3. Recent creation of holder accounts

	similarityScore := 0.0
	for i := 1; i < len(holders) && i < 10; i++ {
		ratio := float64(holders[i].Amount) / float64(holders[0].Amount)
		if ratio > 0.8 && ratio < 1.2 {
			similarityScore += 0.1
		}
	}

	return similarityScore
}

func sendTokenAnalysisEmbed(s *discordgo.Session, channelID, address string, analysis *TokenAnalysis, reference *discordgo.MessageReference) {
	embed := &discordgo.MessageEmbed{
		Title:       "Token Analysis",
		Description: fmt.Sprintf("Analysis for token: `%s`", address),
		Color:       0x1E88E5,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Supply Distribution",
				Value:  fmt.Sprintf("Total Supply: %s\nDecimals: %d\nHolder Count: %d", 
					formatTokenAmount(analysis.TotalSupply, analysis.Decimals),
					analysis.Decimals,
					analysis.HolderCount),
				Inline: false,
			},
			{
				Name:   "Top Holders",
				Value:  formatTopHoldersWithDecimals(analysis.TopHolders, analysis.Decimals),
				Inline: false,
			},
			{
				Name:   "Insider Ownership",
				Value:  fmt.Sprintf("%.2f%% held by top 5 wallets", analysis.InsiderPercent),
				Inline: false,
			},
			{
				Name:   "Bundling Risk",
				Value:  fmt.Sprintf("Score: %.2f/1.0", analysis.BundlingScore),
				Inline: false,
			},
		},
	}

	// Add warnings if any
	if len(analysis.SuspiciousFlags) > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "⚠️ Warnings",
			Value:  "• " + strings.Join(analysis.SuspiciousFlags, "\n• "),
			Inline: false,
		})
	}

	// Add explorer links
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

func formatTopHoldersWithDecimals(holders []TokenHolder, decimals uint8) string {
	var sb strings.Builder
	for i, holder := range holders {
		sb.WriteString(fmt.Sprintf("%d. `%s`: %s (%0.2f%%)\n",
			i+1,
			truncateAddress(holder.Address),
			formatTokenAmount(holder.Amount, decimals),
			holder.Percent))
	}
	return sb.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Helper function to format numbers with commas
func formatNumber(n uint64) string {
	in := strconv.FormatUint(n, 10)
	numOfDigits := len(in)
	if n < 0 {
		numOfDigits-- // First character is the - sign (not a digit)
	}
	numOfCommas := (numOfDigits - 1) / 3

	out := make([]byte, len(in)+numOfCommas)
	if n < 0 {
		in = in[1:]
		out[0] = '-'
	}

	for i, j, k := len(in)-1, len(out)-1, 0; ; i, j = i-1, j-1 {
		out[j] = in[i]
		if i == 0 {
			break
		}
		k++
		if k == 3 {
			j--
			out[j] = ','
			k = 0
		}
	}
	return string(out)
}

// Helper function to truncate Solana addresses
func truncateAddress(address string) string {
	if len(address) <= 12 {
		return address
	}
	return address[:6] + "..." + address[len(address)-4:]
}