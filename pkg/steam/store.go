package steam

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Store endpoints are public (no API key) but undocumented and rate limited,
// so callers should cache results.

// Price amounts are in the currency's minor unit (e.g. paise for INR).
type Price struct {
	Currency        string `json:"currency"`
	Initial         int    `json:"initial"`
	Final           int    `json:"final"`
	DiscountPercent int    `json:"discount_percent"`
	FinalFormatted  string `json:"final_formatted"`
	// Unix seconds; 0 when not discounted or unknown.
	DiscountEndsAt int64 `json:"discount_ends_at"`
}

type StoreItem struct {
	AppID       int    `json:"app_id"`
	Name        string `json:"name"`
	HeaderImage string `json:"header_image"`
	StoreURL    string `json:"store_url"`
	IsFree      bool   `json:"is_free"`
	// Nil when the game is not purchasable (e.g. unreleased).
	Price *Price `json:"price"`
}

const storeAssetBase = "https://shared.akamai.steamstatic.com/store_item_assets/"

func storeURL(appID int) string {
	return "https://store.steampowered.com/app/" + strconv.Itoa(appID)
}

// GetStoreItems looks up names, header art and regional prices for up to a
// few hundred apps in one request. countryCode is ISO 3166-1 alpha-2.
func (c *Client) GetStoreItems(appIDs []int, countryCode string) (map[int]StoreItem, error) {
	items := make(map[int]StoreItem, len(appIDs))
	if len(appIDs) == 0 {
		return items, nil
	}

	type id struct {
		AppID int `json:"appid"`
	}
	ids := make([]id, len(appIDs))
	for i, a := range appIDs {
		ids[i] = id{a}
	}
	input, err := json.Marshal(map[string]any{
		"ids":          ids,
		"context":      map[string]string{"language": "english", "country_code": strings.ToUpper(countryCode)},
		"data_request": map[string]bool{"include_assets": true},
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Response struct {
			StoreItems []struct {
				AppID   int    `json:"appid"`
				Success int    `json:"success"`
				Name    string `json:"name"`
				IsFree  bool   `json:"is_free"`
				Assets  struct {
					URLFormat string `json:"asset_url_format"`
					Header    string `json:"header"`
				} `json:"assets"`
				BestPurchase *struct {
					Final       string `json:"final_price_in_cents"`
					Original    string `json:"original_price_in_cents"`
					Formatted   string `json:"formatted_final_price"`
					DiscountPct int    `json:"discount_pct"`
					Discounts   []struct {
						EndDate int64 `json:"discount_end_date"`
					} `json:"active_discounts"`
				} `json:"best_purchase_option"`
			} `json:"store_items"`
		} `json:"response"`
	}
	params := url.Values{"input_json": {string(input)}}
	if err := c.getJSON("https://api.steampowered.com/IStoreBrowseService/GetItems/v1/", params, &result); err != nil {
		return nil, err
	}

	for _, it := range result.Response.StoreItems {
		if it.Success != 1 {
			continue
		}
		item := StoreItem{AppID: it.AppID, Name: it.Name, IsFree: it.IsFree, StoreURL: storeURL(it.AppID)}
		if it.Assets.Header != "" {
			item.HeaderImage = storeAssetBase + strings.Replace(it.Assets.URLFormat, "${FILENAME}", it.Assets.Header, 1)
		}
		if bp := it.BestPurchase; bp != nil {
			final, _ := strconv.Atoi(bp.Final)
			initial, err := strconv.Atoi(bp.Original)
			if err != nil {
				initial = final
			}
			p := &Price{Currency: currencyFor(countryCode), Initial: initial, Final: final, DiscountPercent: bp.DiscountPct, FinalFormatted: bp.Formatted}
			if len(bp.Discounts) > 0 {
				p.DiscountEndsAt = bp.Discounts[0].EndDate
			}
			item.Price = p
		}
		items[it.AppID] = item
	}
	return items, nil
}

// GetDeals returns the discounted games Steam is featuring for a country,
// merged from the store front's specials, featured, top-seller and
// new-release lists.
func (c *Client) GetDeals(countryCode string) ([]StoreItem, error) {
	type rawItem struct {
		ID                 int    `json:"id"`
		Name               string `json:"name"`
		Discounted         bool   `json:"discounted"`
		DiscountPercent    int    `json:"discount_percent"`
		OriginalPrice      int    `json:"original_price"`
		FinalPrice         int    `json:"final_price"`
		Currency           string `json:"currency"`
		HeaderImage        string `json:"header_image"`
		DiscountExpiration int64  `json:"discount_expiration"`
	}
	type list struct {
		Items []rawItem `json:"items"`
	}
	var categories struct {
		Specials    list `json:"specials"`
		TopSellers  list `json:"top_sellers"`
		NewReleases list `json:"new_releases"`
	}
	var featured struct {
		FeaturedWin []rawItem `json:"featured_win"`
	}

	params := url.Values{"cc": {strings.ToLower(countryCode)}, "l": {"english"}}
	if err := c.getJSON("https://store.steampowered.com/api/featuredcategories", params, &categories); err != nil {
		return nil, err
	}
	// The featured list only adds variety; the deals are still useful without it.
	_ = c.getJSON("https://store.steampowered.com/api/featured", params, &featured)

	var deals []StoreItem
	seen := map[int]bool{}
	for _, group := range [][]rawItem{categories.Specials.Items, featured.FeaturedWin, categories.TopSellers.Items, categories.NewReleases.Items} {
		for _, it := range group {
			if !it.Discounted || it.DiscountPercent <= 0 || seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			deals = append(deals, StoreItem{
				AppID:       it.ID,
				Name:        it.Name,
				HeaderImage: it.HeaderImage,
				StoreURL:    storeURL(it.ID),
				Price: &Price{
					Currency:        it.Currency,
					Initial:         it.OriginalPrice,
					Final:           it.FinalPrice,
					DiscountPercent: it.DiscountPercent,
					DiscountEndsAt:  it.DiscountExpiration,
				},
			})
		}
	}
	return deals, nil
}

// GetItems doesn't report a currency, so infer it from the store country.
func currencyFor(countryCode string) string {
	code := strings.ToUpper(countryCode)
	if euroCountries[code] {
		return "EUR"
	}
	return storeCurrencies[code]
}

// Argentina and Turkey were moved to USD pricing by Steam in 2023.
var storeCurrencies = map[string]string{
	"AE": "AED", "AR": "USD", "AU": "AUD", "BR": "BRL", "CA": "CAD", "CH": "CHF", "CN": "CNY",
	"GB": "GBP", "HK": "HKD", "ID": "IDR", "IN": "INR", "JP": "JPY", "KR": "KRW", "MX": "MXN",
	"MY": "MYR", "NO": "NOK", "NZ": "NZD", "PH": "PHP", "PL": "PLN", "SA": "SAR", "SG": "SGD",
	"TH": "THB", "TR": "USD", "TW": "TWD", "US": "USD", "VN": "VND", "ZA": "ZAR",
}

var euroCountries = map[string]bool{
	"AT": true, "BE": true, "CY": true, "DE": true, "EE": true, "ES": true, "FI": true, "FR": true, "GR": true,
	"IE": true, "IT": true, "LT": true, "LU": true, "LV": true, "MT": true, "NL": true, "PT": true, "SI": true,
	"SK": true,
}
