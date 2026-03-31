package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/marketplaceagreement"
	mpatypes "github.com/aws/aws-sdk-go-v2/service/marketplaceagreement/types"
)

var version = "dev"

func main() {
	commitment := flag.Float64("commitment", 1000000, "EDP committed spend in USD")
	start := flag.String("start", "2024-01-01", "EDP period start (YYYY-MM-DD)")
	end := flag.String("end", "2024-12-31", "EDP period end (YYYY-MM-DD)")
	profile := flag.String("profile", "", "AWS profile to use")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Printf("aws-shortfall version %s\n", version)
		os.Exit(0)
	}

	periodEnd, err := time.Parse("2006-01-02", *end)
	if err != nil {
		log.Fatalf("invalid end date: %v", err)
	}

	// Cost Explorer end date is exclusive, so add one day
	ceEnd := periodEnd.AddDate(0, 0, 1).Format("2006-01-02")

	// Cap the end date to today if the period hasn't finished yet
	today := time.Now().Format("2006-01-02")
	if ceEnd > today {
		ceEnd = today
	}

	ctx := context.Background()

	var cfgOpts []func(*config.LoadOptions) error
	if *profile != "" {
		cfgOpts = append(cfgOpts, config.WithSharedConfigProfile(*profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		log.Fatalf("unable to load AWS config: %v", err)
	}

	ce := costexplorer.NewFromConfig(cfg)

	// Marketplace Agreement API is only available in us-east-1
	mpaCfg, err := config.LoadDefaultConfig(ctx, append(cfgOpts, config.WithRegion("us-east-1"))...)
	if err != nil {
		log.Fatalf("unable to load AWS config for us-east-1: %v", err)
	}
	mpa := marketplaceagreement.NewFromConfig(mpaCfg)

	// --- 1. Get total unblended cost excluding Tax and Extended Support ---
	totalCost, monthlyCosts, err := getEDPEligibleCosts(ctx, ce, *start, ceEnd)
	if err != nil {
		log.Fatalf("failed to get eligible costs: %v", err)
	}

	// --- 2. Get EDP discount credits (negative line items) to add back ---
	edpCredits, err := getEDPDiscountCredits(ctx, ce, *start, ceEnd)
	if err != nil {
		log.Fatalf("failed to get EDP credits: %v", err)
	}

	// --- 3. Get marketplace costs and classify via Agreement API ---
	mpProducts, err := getMarketplaceCosts(ctx, ce, *start, ceEnd)
	if err != nil {
		log.Fatalf("failed to get marketplace costs: %v", err)
	}

	agreements, err := getActiveAgreements(ctx, mpa)
	if err != nil {
		log.Printf("Warning: could not fetch marketplace agreements: %v", err)
	} else {
		classifyFromAgreements(ctx, mpa, mpProducts, agreements)

		// Get "Deployed on AWS" status from Discovery API
		err = enrichFromDiscoveryAPI(ctx, cfg, mpProducts, agreements)
		if err != nil {
			log.Printf("Warning: could not auto-detect deployment status: %v", err)
		}
	}

	var mpExcluded float64
	for _, p := range mpProducts {
		if !p.onAWS {
			mpExcluded += p.cost
		}
	}

	// EDP-eligible spend = total - EDP discounts added back - non-AWS marketplace
	edpEligibleSpend := totalCost - edpCredits - mpExcluded

	shortfall := *commitment - edpEligibleSpend

	// --- Output ---
	fmt.Println()
	fmt.Println("=== AWS EDP Shortfall Calculator ===")
	fmt.Println()
	fmt.Printf("EDP Period:     %s to %s\n", *start, *end)
	fmt.Printf("Data through:   %s\n", ceEnd)
	fmt.Printf("Commitment:     $%s\n", formatMoney(*commitment))
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Month\tEligible Spend")
	fmt.Fprintln(w, "-----\t--------------")
	for _, mc := range monthlyCosts {
		fmt.Fprintf(w, "%s\t$%s\n", mc.month, formatMoney(mc.amount))
	}
	w.Flush()

	// --- Marketplace breakdown ---
	if len(mpProducts) > 0 {
		fmt.Println()
		fmt.Println("--- Marketplace Products ---")
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Product\tCost\tDelivery\tOn AWS?\tCounts?")
		fmt.Fprintln(w, "-------\t----\t--------\t-------\t-------")

		sorted := make([]*marketplaceProduct, 0, len(mpProducts))
		for _, p := range mpProducts {
			sorted = append(sorted, p)
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].cost > sorted[j].cost
		})

		for _, p := range sorted {
			counts := "YES"
			if !p.onAWS {
				counts = "NO"
			}
			fmt.Fprintf(w, "%s\t$%s\t%s\t%v\t%s\n",
				truncate(p.name, 50), formatMoney(p.cost), p.deliveryMethod, p.onAWS, counts)
		}
		w.Flush()
	}

	fmt.Println()
	fmt.Printf("Total eligible spend (excl. Tax & Extended Support):  $%s\n", formatMoney(totalCost))
	fmt.Printf("EDP discount credits added back:                      $%s\n", formatMoney(-edpCredits))
	if mpExcluded > 0 {
		fmt.Printf("Marketplace (not on AWS) excluded:                    -$%s\n", formatMoney(mpExcluded))
	}
	fmt.Printf("EDP-countable spend:                                  $%s\n", formatMoney(edpEligibleSpend))
	fmt.Println()

	if shortfall > 0 {
		fmt.Printf("SHORTFALL:  $%s\n", formatMoney(shortfall))
		fmt.Printf("Coverage:   %.1f%%\n", (edpEligibleSpend / *commitment)*100)
	} else {
		fmt.Printf("ON TRACK - surplus of $%s\n", formatMoney(-shortfall))
	}

	// --- Projection ---
	projectedSpend, projectedShortfall := projectSpend(*start, *end, ceEnd, edpEligibleSpend, *commitment)
	if projectedSpend > 0 {
		fmt.Println()
		fmt.Println("--- Projection (linear extrapolation) ---")
		fmt.Printf("Projected total spend:      $%s\n", formatMoney(projectedSpend))
		if projectedShortfall > 0 {
			fmt.Printf("Projected shortfall:        $%s\n", formatMoney(projectedShortfall))
		} else {
			fmt.Printf("Projected surplus:          $%s\n", formatMoney(-projectedShortfall))
		}
	}

	fmt.Println()
}

type monthlyCost struct {
	month  string
	amount float64
}

type marketplaceProduct struct {
	name           string
	cost           float64
	usageTypes     map[string]bool // usage types seen in billing
	deliveryMethod string          // "AMI", "Container", "SaaS", "Data", "Unknown"
	onAWS          bool
}

type agreement struct {
	id           string
	resourceType string // "AmiProduct", "ContainerProduct", "SaaSProduct", "DataProduct"
	resourceID   string
	dimKeys      []string // dimension keys from agreement terms
}

func getEDPEligibleCosts(ctx context.Context, ce *costexplorer.Client, start, end string) (float64, []monthlyCost, error) {
	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start),
			End:   aws.String(end),
		},
		Granularity: types.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		Filter: &types.Expression{
			Not: &types.Expression{
				Dimensions: &types.DimensionValues{
					Key:    types.DimensionRecordType,
					Values: []string{"Tax"},
				},
			},
		},
	}

	resp, err := ce.GetCostAndUsage(ctx, input)
	if err != nil {
		return 0, nil, fmt.Errorf("GetCostAndUsage (excl tax): %w", err)
	}

	var total float64
	var monthly []monthlyCost

	for _, result := range resp.ResultsByTime {
		amount := parseAmount(result.Total["UnblendedCost"].Amount)
		month := *result.TimePeriod.Start
		monthly = append(monthly, monthlyCost{month: month[:7], amount: amount})
		total += amount
	}

	extSupportCost, err := getExtendedSupportCosts(ctx, ce, start, end)
	if err != nil {
		return 0, nil, fmt.Errorf("extended support costs: %w", err)
	}

	total -= extSupportCost

	return total, monthly, nil
}

func getExtendedSupportCosts(ctx context.Context, ce *costexplorer.Client, start, end string) (float64, error) {
	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start),
			End:   aws.String(end),
		},
		Granularity: types.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		Filter: &types.Expression{
			Not: &types.Expression{
				Dimensions: &types.DimensionValues{
					Key:    types.DimensionRecordType,
					Values: []string{"Tax"},
				},
			},
		},
		GroupBy: []types.GroupDefinition{
			{
				Type: types.GroupDefinitionTypeDimension,
				Key:  aws.String("USAGE_TYPE"),
			},
		},
	}

	var total float64
	var nextToken *string

	for {
		input.NextPageToken = nextToken
		resp, err := ce.GetCostAndUsage(ctx, input)
		if err != nil {
			return 0, fmt.Errorf("GetCostAndUsage (extended support): %w", err)
		}

		for _, result := range resp.ResultsByTime {
			for _, group := range result.Groups {
				for _, key := range group.Keys {
					if strings.Contains(strings.ToLower(key), "extended-support") ||
						strings.Contains(strings.ToLower(key), "extendedsupport") {
						amount := parseAmount(group.Metrics["UnblendedCost"].Amount)
						total += amount
					}
				}
			}
		}

		nextToken = resp.NextPageToken
		if nextToken == nil {
			break
		}
	}

	if total > 0 {
		fmt.Printf("Note: Found $%s in Extended Support charges (excluded from EDP)\n", formatMoney(total))
	}

	return total, nil
}

func getEDPDiscountCredits(ctx context.Context, ce *costexplorer.Client, start, end string) (float64, error) {
	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start),
			End:   aws.String(end),
		},
		Granularity: types.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		Filter: &types.Expression{
			Dimensions: &types.DimensionValues{
				Key:    types.DimensionRecordType,
				Values: []string{"EdpDiscount", "PrivateRateDiscount", "BundledDiscount"},
			},
		},
	}

	resp, err := ce.GetCostAndUsage(ctx, input)
	if err != nil {
		return 0, fmt.Errorf("GetCostAndUsage (EDP credits): %w", err)
	}

	var total float64
	for _, result := range resp.ResultsByTime {
		amount := parseAmount(result.Total["UnblendedCost"].Amount)
		total += amount
	}

	return total, nil
}

func getMarketplaceCosts(ctx context.Context, ce *costexplorer.Client, start, end string) (map[string]*marketplaceProduct, error) {
	// Get marketplace costs grouped by SERVICE and USAGE_TYPE
	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start),
			End:   aws.String(end),
		},
		Granularity: types.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		Filter: &types.Expression{
			And: []types.Expression{
				{
					Not: &types.Expression{
						Dimensions: &types.DimensionValues{
							Key:    types.DimensionRecordType,
							Values: []string{"Tax"},
						},
					},
				},
				{
					Dimensions: &types.DimensionValues{
						Key:    types.DimensionBillingEntity,
						Values: []string{"AWS Marketplace"},
					},
				},
			},
		},
		GroupBy: []types.GroupDefinition{
			{
				Type: types.GroupDefinitionTypeDimension,
				Key:  aws.String("SERVICE"),
			},
			{
				Type: types.GroupDefinitionTypeDimension,
				Key:  aws.String("USAGE_TYPE"),
			},
		},
	}

	products := make(map[string]*marketplaceProduct)
	var nextToken *string

	for {
		input.NextPageToken = nextToken
		resp, err := ce.GetCostAndUsage(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("GetCostAndUsage (marketplace): %w", err)
		}

		for _, result := range resp.ResultsByTime {
			for _, group := range result.Groups {
				name := group.Keys[0]
				usageType := group.Keys[1]
				amount := parseAmount(group.Metrics["UnblendedCost"].Amount)
				if p, ok := products[name]; ok {
					p.cost += amount
					p.usageTypes[usageType] = true
				} else {
					products[name] = &marketplaceProduct{
						name:           name,
						cost:           amount,
						usageTypes:     map[string]bool{usageType: true},
						deliveryMethod: "Unknown",
						onAWS:          true, // default: count it
					}
				}
			}
		}

		nextToken = resp.NextPageToken
		if nextToken == nil {
			break
		}
	}

	return products, nil
}

func getActiveAgreements(ctx context.Context, mpa *marketplaceagreement.Client) ([]agreement, error) {
	fmt.Println("Fetching marketplace agreements...")

	input := &marketplaceagreement.SearchAgreementsInput{
		Catalog: aws.String("AWSMarketplace"),
		Filters: []mpatypes.Filter{
			{Name: aws.String("PartyType"), Values: []string{"Acceptor"}},
			{Name: aws.String("AgreementType"), Values: []string{"PurchaseAgreement"}},
			{Name: aws.String("Status"), Values: []string{"ACTIVE"}},
		},
		MaxResults: aws.Int32(50),
	}

	var agreements []agreement
	var nextToken *string

	for {
		input.NextToken = nextToken
		resp, err := mpa.SearchAgreements(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("SearchAgreements: %w", err)
		}

		for _, a := range resp.AgreementViewSummaries {
			agmt := agreement{
				id: aws.ToString(a.AgreementId),
			}
			if a.ProposalSummary != nil && len(a.ProposalSummary.Resources) > 0 {
				res := a.ProposalSummary.Resources[0]
				agmt.resourceType = aws.ToString(res.Type)
				agmt.resourceID = aws.ToString(res.Id)
			}
			agreements = append(agreements, agmt)
		}

		nextToken = resp.NextToken
		if nextToken == nil {
			break
		}
	}

	// Fetch dimension keys from agreement terms
	for i := range agreements {
		terms, err := mpa.GetAgreementTerms(ctx, &marketplaceagreement.GetAgreementTermsInput{
			AgreementId: aws.String(agreements[i].id),
		})
		if err != nil {
			continue
		}
		agreements[i].dimKeys = extractDimensionKeys(terms)
	}

	return agreements, nil
}

func extractDimensionKeys(terms *marketplaceagreement.GetAgreementTermsOutput) []string {
	var keys []string
	for _, term := range terms.AcceptedTerms {
		switch t := term.(type) {
		case *mpatypes.AcceptedTermMemberFixedUpfrontPricingTerm:
			for _, g := range t.Value.Grants {
				if g.DimensionKey != nil {
					keys = append(keys, *g.DimensionKey)
				}
			}
		case *mpatypes.AcceptedTermMemberUsageBasedPricingTerm:
			for _, rc := range t.Value.RateCards {
				for _, r := range rc.RateCard {
					if r.DimensionKey != nil {
						keys = append(keys, *r.DimensionKey)
					}
				}
			}
		case *mpatypes.AcceptedTermMemberConfigurableUpfrontPricingTerm:
			for _, rc := range t.Value.RateCards {
				for _, r := range rc.RateCard {
					if r.DimensionKey != nil {
						keys = append(keys, *r.DimensionKey)
					}
				}
			}
		}
	}
	return keys
}

// classifyFromAgreements matches billing products to marketplace agreements
// using dimension keys found in agreement terms vs usage types in billing.
// This sets the delivery method (AMI/Container/SaaS/Data) but does NOT
// determine on-AWS status — that comes from the --exclude-products flag.
func classifyFromAgreements(ctx context.Context, mpa *marketplaceagreement.Client, products map[string]*marketplaceProduct, agreements []agreement) {
	fmt.Printf("Found %d active agreements, matching to %d billing products...\n", len(agreements), len(products))

	matched := make(map[string]bool)

	// Pass 1: match agreements to products via unique dimension keys
	for _, agmt := range agreements {
		deliveryLabel := deliveryLabelFor(agmt.resourceType)

		for name, product := range products {
			if matched[name] {
				continue
			}
			if matchAgreementToProduct(agmt, product) {
				product.deliveryMethod = deliveryLabel
				matched[name] = true
				break
			}
		}
	}

	// Pass 2: unmatched products with only generic contract usage types are SaaS
	for name, product := range products {
		if matched[name] {
			continue
		}
		if isGenericContractProduct(product) {
			product.deliveryMethod = "SaaS"
		}
	}
}

type discoveryListing struct {
	ID      string `json:"id"`
	Summary struct {
		Badges []struct {
			DisplayName string `json:"displayName"`
			Value       string `json:"value"`
		} `json:"badges"`
		DisplayAttributes struct {
			Title string `json:"title"`
		} `json:"displayAttributes"`
		ProductAttributes struct {
			BaseProductID string `json:"baseProductId"`
		} `json:"productAttributes"`
	} `json:"summary"`
}

type discoveryResponse struct {
	ListingView string `json:"ListingView"`
}

type discoveryListingView struct {
	Data struct {
		Listings []discoveryListing `json:"listings"`
	} `json:"data"`
}

func enrichFromDiscoveryAPI(ctx context.Context, cfg aws.Config, products map[string]*marketplaceProduct, agreements []agreement) error {
	// Build list of product IDs from agreements
	productIDs := make([]string, 0, len(agreements))
	for _, agmt := range agreements {
		if agmt.resourceID != "" {
			productIDs = append(productIDs, agmt.resourceID)
		}
	}

	if len(productIDs) == 0 {
		return nil
	}

	// Prepare parameters as JSON string (not object)
	parametersObj := map[string]interface{}{
		"productIds": productIDs,
	}
	parametersJSON, err := json.Marshal(parametersObj)
	if err != nil {
		return fmt.Errorf("marshal parameters: %w", err)
	}

	// Prepare request body matching console format
	requestBody := map[string]interface{}{
		"ViewQuery": map[string]interface{}{
			"Name":    "listingSummariesByProductIds",
			"Version": 1,
		},
		"Parameters": string(parametersJSON),
		"RequestContext": map[string]interface{}{
			"IntegrationId": "integ-236lto4nvn4as",
		},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	// Create HTTP request
	endpoint := "https://discovery.marketplace.us-east-1.amazonaws.com/GetListingView"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Sign the request with AWS SigV4
	payloadHash := sha256.Sum256(bodyBytes)
	signer := v4.NewSigner()
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("retrieve credentials: %w", err)
	}

	err = signer.SignHTTP(ctx, creds, req, hex.EncodeToString(payloadHash[:]), "aws-marketplace", "us-east-1", time.Now())
	if err != nil {
		return fmt.Errorf("sign request: %w", err)
	}

	// Execute request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var discoveryResp discoveryResponse
	if err := json.Unmarshal(body, &discoveryResp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	// Parse the nested JSON string
	var listingView discoveryListingView
	if err := json.Unmarshal([]byte(discoveryResp.ListingView), &listingView); err != nil {
		return fmt.Errorf("unmarshal listing view: %w", err)
	}

	// Create maps of baseProductId -> hasDeployedOnAWSBadge and baseProductId -> productName
	deploymentStatus := make(map[string]bool)
	productNames := make(map[string]string)
	for _, listing := range listingView.Data.Listings {
		baseProductID := listing.Summary.ProductAttributes.BaseProductID
		productName := listing.Summary.DisplayAttributes.Title
		hasDeployedBadge := false
		for _, badge := range listing.Summary.Badges {
			if badge.Value == "DEPLOYED_ON_AWS" {
				hasDeployedBadge = true
				break
			}
		}
		deploymentStatus[baseProductID] = hasDeployedBadge
		productNames[baseProductID] = productName
	}

	// Update products with deployment status
	for _, agmt := range agreements {
		if agmt.resourceID == "" {
			continue
		}

		// Find matching product by name (since we don't have direct baseProductId mapping in billing)
		hasDeployedBadge, found := deploymentStatus[agmt.resourceID]
		if !found {
			continue
		}

		productName := productNames[agmt.resourceID]

		// Match agreement to product in billing data
		// Try dimension key matching first
		matched := false
		for _, product := range products {
			if matchAgreementToProduct(agmt, product) {
				matched = true
				// Only update if the product DOESN'T have the badge (i.e., NOT deployed on AWS)
				if !hasDeployedBadge {
					product.onAWS = false
				}
				break
			}
		}

		// If no match found by dimension keys, try matching by product name
		if !matched && productName != "" {
			for _, product := range products {
				// Case-insensitive substring match
				if strings.Contains(strings.ToLower(product.name), strings.ToLower(productName)) ||
					strings.Contains(strings.ToLower(productName), strings.ToLower(product.name)) {
					matched = true
					if !hasDeployedBadge {
						product.onAWS = false
					}
					break
				}
			}
		}
	}

	return nil
}

func matchAgreementToProduct(agmt agreement, product *marketplaceProduct) bool {
	for _, dimKey := range agmt.dimKeys {
		for usageType := range product.usageTypes {
			// Billing usage types look like "MP:NR_APoF_Overage-Units" or
			// "USE1-MP:USE1_InputTokenCount-Units" or "Global-SoftwareUsage-Contracts".
			// Dimension keys look like "NR_APoF_Overage" or "USE1_InputTokenCount".
			// Match if the usage type contains the dimension key.
			if strings.Contains(usageType, dimKey) {
				return true
			}
		}
	}
	return false
}

func isGenericContractProduct(product *marketplaceProduct) bool {
	for ut := range product.usageTypes {
		if !strings.Contains(ut, "SoftwareUsage-Contracts") {
			return false
		}
	}
	return len(product.usageTypes) > 0
}

func deliveryLabelFor(resourceType string) string {
	switch resourceType {
	case "AmiProduct":
		return "AMI"
	case "ContainerProduct":
		return "Container"
	case "SaaSProduct":
		return "SaaS"
	case "DataProduct":
		return "Data"
	default:
		return "Unknown"
	}
}

func projectSpend(start, end, dataEnd string, currentSpend, commitment float64) (float64, float64) {
	startDate, _ := time.Parse("2006-01-02", start)
	endDate, _ := time.Parse("2006-01-02", end)
	dataEndDate, _ := time.Parse("2006-01-02", dataEnd)

	totalDays := endDate.Sub(startDate).Hours() / 24
	elapsedDays := dataEndDate.Sub(startDate).Hours() / 24

	if elapsedDays <= 0 || totalDays <= 0 {
		return 0, 0
	}

	dailyRate := currentSpend / elapsedDays
	projectedTotal := dailyRate * totalDays
	projectedShortfall := commitment - projectedTotal

	return projectedTotal, projectedShortfall
}

func parseAmount(s *string) float64 {
	if s == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(*s, 64)
	return f
}

func formatMoney(amount float64) string {
	negative := amount < 0
	if negative {
		amount = -amount
	}

	whole := int64(amount)
	cents := int64((amount-float64(whole))*100 + 0.5)
	if cents >= 100 {
		whole++
		cents -= 100
	}

	s := fmt.Sprintf("%d", whole)
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}

	formatted := fmt.Sprintf("%s.%02d", string(result), cents)
	if negative {
		return "-" + formatted
	}
	return formatted
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
