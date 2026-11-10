package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

const fuelCardLitreTolerance = 0.05

// NewFuelCardResource is the fuel card registry (migration 0065).
func NewFuelCardResource(repo *store.Repository) *Resource[models.FuelCard, *models.FuelCard] {
	r := &Resource[models.FuelCard, *models.FuelCard]{
		Repo: repo, Collection: repo.FuelCards,
		Entity: "fuel_card", IDPrefix: "FCARD",
	}
	hook := func(_ *gin.Context, c *models.FuelCard) error {
		return normalizeFuelCard(c)
	}
	r.BeforeCreate, r.BeforeUpdate = hook, hook
	return r
}

func normalizeFuelCard(c *models.FuelCard) error {
	c.CardNumber = strings.TrimSpace(c.CardNumber)
	if c.CardNumber == "" {
		return fmt.Errorf("cardNumber is required")
	}
	c.Provider = strings.TrimSpace(c.Provider)
	c.Label = strings.TrimSpace(c.Label)
	c.Currency = strings.TrimSpace(c.Currency)
	if c.Currency == "" {
		c.Currency = "UGX"
	}
	c.VehicleID = strings.TrimSpace(c.VehicleID)
	c.DriverID = strings.TrimSpace(c.DriverID)
	c.Status = strings.TrimSpace(c.Status)
	if c.Status == "" {
		c.Status = "active"
	}
	switch c.Status {
	case "active", "blocked", "expired", "cancelled":
	default:
		return fmt.Errorf("status must be active, blocked, expired, or cancelled")
	}
	c.Last4 = last4FromCardNumber(c.CardNumber)
	if c.Status == "active" {
		hasV, hasD := c.VehicleID != "", c.DriverID != ""
		if hasV == hasD {
			return fmt.Errorf("active cards must be assigned to exactly one of vehicleId or driverId")
		}
	}
	return nil
}

func last4FromCardNumber(num string) string {
	digits := make([]rune, 0, len(num))
	for _, r := range num {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
		}
	}
	if len(digits) == 0 {
		clean := strings.TrimSpace(num)
		if len(clean) <= 4 {
			return clean
		}
		return clean[len(clean)-4:]
	}
	if len(digits) <= 4 {
		return string(digits)
	}
	return string(digits[len(digits)-4:])
}

// NewFuelCardTransactionResource stores pump fills and matches them to
// requests/logs on write.
func NewFuelCardTransactionResource(repo *store.Repository) *Resource[models.FuelCardTransaction, *models.FuelCardTransaction] {
	r := &Resource[models.FuelCardTransaction, *models.FuelCardTransaction]{
		Repo: repo, Collection: repo.FuelCardTransactions,
		Entity: "fuel_card_transaction", IDPrefix: "FCTXN",
	}
	hook := func(c *gin.Context, t *models.FuelCardTransaction) error {
		return prepareFuelCardTransaction(c.Request.Context(), repo, t)
	}
	r.BeforeCreate, r.BeforeUpdate = hook, hook
	return r
}

func prepareFuelCardTransaction(ctx context.Context, repo *store.Repository, t *models.FuelCardTransaction) error {
	t.FuelCardID = strings.TrimSpace(t.FuelCardID)
	if t.FuelCardID == "" {
		return fmt.Errorf("fuelCardId is required")
	}
	t.Date = strings.TrimSpace(t.Date)
	if t.Date == "" {
		return fmt.Errorf("date is required")
	}
	if t.Litres < 0 || t.Amount < 0 {
		return fmt.Errorf("litres and amount must be >= 0")
	}
	t.Station = strings.TrimSpace(t.Station)
	t.Merchant = strings.TrimSpace(t.Merchant)
	t.ExternalRef = strings.TrimSpace(t.ExternalRef)
	t.ImportBatchID = strings.TrimSpace(t.ImportBatchID)
	t.Notes = strings.TrimSpace(t.Notes)
	if t.UnitPrice <= 0 && t.Litres > 0 && t.Amount > 0 {
		t.UnitPrice = math.Round((t.Amount/t.Litres)*100) / 100
	}
	if strings.EqualFold(strings.TrimSpace(t.MatchStatus), "ignored") {
		t.MatchStatus = "ignored"
		t.MatchedFuelRequestID = ""
		t.MatchedFuelRecordID = ""
		return nil
	}
	card, err := repo.FuelCards.Get(ctx, t.FuelCardID)
	if err != nil {
		return fmt.Errorf("fuel card %s not found", t.FuelCardID)
	}
	matchFuelCardTransaction(ctx, repo, &card, t)
	return nil
}

func litresCloseFuel(a, b float64) bool {
	if a <= 0 && b <= 0 {
		return true
	}
	max := math.Max(math.Max(math.Abs(a), math.Abs(b)), 1)
	return math.Abs(a-b)/max <= fuelCardLitreTolerance
}

func matchFuelCardTransaction(ctx context.Context, repo *store.Repository, card *models.FuelCard, t *models.FuelCardTransaction) {
	t.MatchedFuelRequestID = ""
	t.MatchedFuelRecordID = ""
	t.MatchStatus = "unmatched"

	day := t.Date
	if len(day) > 10 {
		day = day[:10]
	}

	var reqID, recID string
	if reqs, err := repo.FuelRequests.List(ctx); err == nil {
		for _, r := range reqs {
			if !fuelAssigneeMatches(card, r.VehicleID, r.DriverID) {
				continue
			}
			rd := strings.TrimSpace(r.NeededBy)
			if rd == "" {
				rd = strings.TrimSpace(r.CreatedAt)
			}
			if len(rd) > 10 {
				rd = rd[:10]
			}
			if rd != "" && !datesWithinOneDay(day, rd) {
				continue
			}
			if !litresCloseFuel(r.RequestedLitres, t.Litres) && t.Litres != 0 {
				continue
			}
			reqID = r.ID
			break
		}
	}
	if recs, err := repo.Fuel.List(ctx); err == nil {
		for _, r := range recs {
			if !fuelAssigneeMatches(card, r.VehicleID, r.DriverID) {
				continue
			}
			rd := strings.TrimSpace(r.Date)
			if len(rd) > 10 {
				rd = rd[:10]
			}
			if rd != "" && !datesWithinOneDay(day, rd) {
				continue
			}
			if !litresCloseFuel(r.Litres, t.Litres) && t.Litres != 0 {
				continue
			}
			recID = r.ID
			break
		}
	}

	t.MatchedFuelRequestID = reqID
	t.MatchedFuelRecordID = recID
	switch {
	case reqID != "" && recID != "":
		t.MatchStatus = "matched"
	case reqID != "" && recID == "":
		t.MatchStatus = "unfulfilled_request"
	default:
		t.MatchStatus = "unmatched"
	}
}

func fuelAssigneeMatches(card *models.FuelCard, vehicleID, driverID string) bool {
	if card.VehicleID != "" {
		return strings.TrimSpace(vehicleID) == card.VehicleID
	}
	if card.DriverID != "" {
		return strings.TrimSpace(driverID) == card.DriverID
	}
	return false
}

func datesWithinOneDay(a, b string) bool {
	if a == b {
		return true
	}
	da, errA := time.Parse("2006-01-02", a)
	db, errB := time.Parse("2006-01-02", b)
	if errA != nil || errB != nil {
		return false
	}
	diff := da.Sub(db)
	if diff < 0 {
		diff = -diff
	}
	return diff <= 24*time.Hour
}

// RegisterFuelCardResources mounts card + transaction CRUD and the import route.
// Import is registered before /:id so Gin does not treat "import" as an id.
func RegisterFuelCardResources(api *gin.RouterGroup, repo *store.Repository) {
	NewFuelCardResource(repo).Register(api, "/fuel-cards")
	txn := NewFuelCardTransactionResource(repo)
	g := api.Group("/fuel-card-transactions")
	view := auth.RequirePerm("view_fuel_card_transaction")
	add := auth.RequirePerm("add_fuel_card_transaction")
	change := auth.RequirePerm("change_fuel_card_transaction")
	del := auth.RequirePerm("delete_fuel_card_transaction")
	g.GET("", view, txn.list)
	g.GET("/search", view, txn.search)
	g.POST("/import", add, func(c *gin.Context) { importFuelCardTransactions(c, repo) })
	g.GET("/:id", view, txn.get)
	g.POST("", add, txn.create)
	g.POST("/bulk", add, txn.bulkCreate)
	g.PUT("/:id", change, txn.replace)
	g.PATCH("/:id", change, txn.patch)
	g.PATCH("/bulk", change, txn.bulkPatch)
	g.DELETE("/:id", del, txn.remove)
	g.DELETE("/bulk", del, txn.bulkDelete)
}

type fuelCardTxnImportRow struct {
	FuelCardID  string  `json:"fuelCardId"`
	CardNumber  string  `json:"cardNumber"`
	Date        string  `json:"date"`
	Litres      float64 `json:"litres"`
	Amount      float64 `json:"amount"`
	UnitPrice   float64 `json:"unitPrice"`
	Station     string  `json:"station"`
	Merchant    string  `json:"merchant"`
	ExternalRef string  `json:"externalRef"`
	Notes       string  `json:"notes"`
}

type fuelCardTxnImportBody struct {
	BatchID string                 `json:"batchId"`
	Rows    []fuelCardTxnImportRow `json:"rows"`
}

func importFuelCardTransactions(c *gin.Context, repo *store.Repository) {
	var body fuelCardTxnImportBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body.Rows) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rows is required"})
		return
	}
	if len(body.Rows) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at most 1000 rows per import"})
		return
	}
	batchID := strings.TrimSpace(body.BatchID)
	if batchID == "" {
		batchID = "imp-" + uuid.NewString()
	}

	cards, err := repo.FuelCards.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	byNumber := map[string]models.FuelCard{}
	for _, card := range cards {
		byNumber[strings.ToLower(strings.TrimSpace(card.CardNumber))] = card
	}

	existing, err := repo.FuelCardTransactions.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	byExternal := map[string]models.FuelCardTransaction{}
	for _, t := range existing {
		if t.ExternalRef != "" {
			byExternal[t.ExternalRef] = t
		}
	}

	created, updated, failed := 0, 0, 0
	errorsOut := make([]string, 0)
	ctx := c.Request.Context()

	for i, row := range body.Rows {
		cardID := strings.TrimSpace(row.FuelCardID)
		if cardID == "" {
			if card, ok := byNumber[strings.ToLower(strings.TrimSpace(row.CardNumber))]; ok {
				cardID = card.ID
			}
		}
		item := models.FuelCardTransaction{
			FuelCardID:    cardID,
			Date:          strings.TrimSpace(row.Date),
			Litres:        row.Litres,
			Amount:        row.Amount,
			UnitPrice:     row.UnitPrice,
			Station:       strings.TrimSpace(row.Station),
			Merchant:      strings.TrimSpace(row.Merchant),
			ExternalRef:   strings.TrimSpace(row.ExternalRef),
			ImportBatchID: batchID,
			Notes:         strings.TrimSpace(row.Notes),
		}
		if err := prepareFuelCardTransaction(ctx, repo, &item); err != nil {
			failed++
			errorsOut = append(errorsOut, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		if item.ExternalRef != "" {
			if prev, ok := byExternal[item.ExternalRef]; ok {
				item.ID = prev.ID
				if _, err := repo.FuelCardTransactions.Replace(ctx, prev.ID, item); err != nil {
					failed++
					errorsOut = append(errorsOut, fmt.Sprintf("row %d: %v", i+1, err))
					continue
				}
				updated++
				continue
			}
		}
		item.ID = uuid.NewString()
		if _, err := repo.FuelCardTransactions.Add(ctx, item); err != nil {
			failed++
			errorsOut = append(errorsOut, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		created++
		if item.ExternalRef != "" {
			byExternal[item.ExternalRef] = item
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"batchId": batchID,
		"created": created,
		"updated": updated,
		"failed":  failed,
		"errors":  errorsOut,
	})
}

func fillFuelCardLast4(ctx context.Context, repo *store.Repository, rec *models.FuelRecord) {
	cardID := strings.TrimSpace(rec.FuelCardID)
	if cardID == "" || strings.TrimSpace(rec.CardLast4) != "" {
		return
	}
	card, err := repo.FuelCards.Get(ctx, cardID)
	if err != nil {
		return
	}
	rec.CardLast4 = card.Last4
}

// validateFuelCardLink ensures an optional fuelCardId on a request/log matches
// the vehicle/driver and is active.
func validateFuelCardLink(ctx context.Context, repo *store.Repository, cardID, vehicleID, driverID string) error {
	cardID = strings.TrimSpace(cardID)
	if cardID == "" {
		return nil
	}
	card, err := repo.FuelCards.Get(ctx, cardID)
	if err != nil {
		return fmt.Errorf("fuel card %s not found", cardID)
	}
	if card.Status != "active" {
		return fmt.Errorf("fuel card %s is %s", cardID, card.Status)
	}
	if card.VehicleID != "" && strings.TrimSpace(vehicleID) != "" && card.VehicleID != strings.TrimSpace(vehicleID) {
		return fmt.Errorf("fuel card is assigned to a different vehicle")
	}
	if card.DriverID != "" && strings.TrimSpace(driverID) != "" && card.DriverID != strings.TrimSpace(driverID) {
		return fmt.Errorf("fuel card is assigned to a different driver")
	}
	return nil
}
