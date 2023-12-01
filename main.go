package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	coinURL        = "https://api.coinbase.com/v2/exchange-rates?currency=USD"
	defaultTimeout = 15 * time.Second
	pennyPlace     = 2
)

var (
	defaultSplit1 = decimal.NewFromFloat(.7)
	defaultSplit2 = decimal.NewFromFloat(.3)
)

// ratesResponse JSON response from coinbase endpoint.
type ratesResponse struct {
	Data ratesData `json:"data"`
}

// ratesData JSON response data from coinbase endpoint.
type ratesData struct {
	Rates    map[string]string `json:"rates"`
	Currency string            `json:"currency"`
}

// symbolSplit represents a symbol and it's desired split of the balance.
type symbolSplit struct {
	symbol string
	split  decimal.Decimal
}

// distribution is the caulated cost and quantity of a symbol.
type distribution struct {
	qty   decimal.Decimal
	funds decimal.Decimal
}

// symbolString converts a distribution to a string, symbol is the symbol used to calculate the distribution.
func (d distribution) symbolString(symbol string) string {
	return fmt.Sprintf("$%s => %s %s", d.funds.StringFixed(pennyPlace), d.qty, symbol)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	balance, symbSplits, err := parseArgs()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	rates, err := requestRates(ctx)
	if err != nil {
		return err
	}

	dists, err := calculateDistributions(rates, balance, symbSplits)
	if err != nil {
		return err
	}
	var totalCost decimal.Decimal
	for _, dist := range dists {
		totalCost = totalCost.Add(dist.funds)
	}
	if !totalCost.Equal(balance) {
		fmt.Printf("Warning: balance '%s' can not be equally split\n", balance)
	}

	distStrings := distributionStrings(dists, symbSplits)
	fmt.Println(strings.Join(distStrings, "\n"))

	return nil
}

// parseArgs parses and validates the given arguments.
// this function could be reworked to allow a dynamic number of symbols and splits.
func parseArgs() (decimal.Decimal, []symbolSplit, error) {
	// expected format is: coinz 100 BTC ETH
	if len(os.Args) != 4 {
		printUsage()
		return decimal.Decimal{}, nil, fmt.Errorf("incorrect number of arguments")
	}
	balanceArg := os.Args[1]
	balance, err := decimal.NewFromString(balanceArg)
	if err != nil {
		return decimal.Decimal{}, nil, fmt.Errorf("failed to parse balance '%s': %w", balanceArg, err)
	}

	if balance.LessThanOrEqual(decimal.Decimal{}) {
		return decimal.Decimal{}, nil, fmt.Errorf("balance of %s is too low to trade", balance)
	}

	if !balance.Equal(balance.Round(pennyPlace)) {
		return decimal.Decimal{}, nil, fmt.Errorf("subpenny quoting is illegal https://www.sec.gov/divisions/marketreg/subpenny612faq.htm  '%s'", balanceArg)
	}

	symbSplits := make([]symbolSplit, 2)
	symbSplits[0] = symbolSplit{symbol: os.Args[2], split: defaultSplit1}
	symbSplits[1] = symbolSplit{symbol: os.Args[3], split: defaultSplit2}

	return balance, symbSplits, nil
}

// requestRates attempts to get exchange rates from coin base.
func requestRates(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, coinURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create coinbase request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request rates: %w", err)
	}

	bodyData, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed status=%d message=%s", resp.StatusCode, bodyData)
	}

	ratesInfo := ratesResponse{}
	err = json.Unmarshal(bodyData, &ratesInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	if ratesInfo.Data.Rates == nil {
		return nil, fmt.Errorf("invalid response data rates do not exist")
	}
	return ratesInfo.Data.Rates, nil
}

func printUsage() {
	fmt.Println("Usage: coniz <AMOUNT_USD> <symbol_1> <symbol2>")
}

// distributionStrings convert distributions map into a slice of string representations.
// The order is determined by the original argument order provided by symbSplits.
func distributionStrings(dists map[string]distribution, symbSplits []symbolSplit) []string {
	retStrings := make([]string, 0, len(dists))
	for _, symbSplit := range symbSplits {
		retStrings = append(retStrings, dists[symbSplit.symbol].symbolString(symbSplit.symbol))
	}
	return retStrings
}

// calculateDistributions calculates to distribution for all symbolSplits.
func calculateDistributions(rates map[string]string, balance decimal.Decimal, symbSplits []symbolSplit) (map[string]distribution, error) {
	retDists := make(map[string]distribution, len(symbSplits))
	for _, symbSplit := range symbSplits {
		symbRate, err := getRate(rates, symbSplit.symbol)
		if err != nil {
			return nil, err
		}
		retDists[symbSplit.symbol] = calculateDistribution(symbRate, balance, symbSplit)
	}
	return retDists, nil
}

// calculateDistribution calculates to distribution for a single symbolSplit.
func calculateDistribution(symbRate, balance decimal.Decimal, symbSplit symbolSplit) distribution {
	var retDist distribution
	retDist.funds = balance.Mul(symbSplit.split).RoundBank(pennyPlace)
	retDist.qty = retDist.funds.Mul(symbRate)
	return retDist
}

// getRate gets the rates for the specified symbol if it exist and is valid.
func getRate(rates map[string]string, symbol string) (decimal.Decimal, error) {
	rateStr, ok := rates[symbol]
	if !ok {
		return decimal.Decimal{}, fmt.Errorf("unable to find rate for symbol %q not found", symbol)
	}
	rate, err := decimal.NewFromString(rateStr)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("failed to parse '%s' rate of '%s': %w", symbol, rateStr, err)
	}
	return rate, nil
}
