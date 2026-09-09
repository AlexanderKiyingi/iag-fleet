// The driver–vehicle authorisation matrix — PRD FR-DRV-04.
//
// Three small configuration tables rather than a compiled rule, because which
// licence class may operate which kind of vehicle differs by jurisdiction and by
// operator. See 0049 and validateDriverDispatch.
package models

// VehicleCategory is a class of vehicle for dispatch purposes ("Rigid truck",
// "Articulated", "PSV minibus"). Distinct from Vehicle.Type, which is free text
// and in practice carries the manufacturer.
type VehicleCategory struct {
	ID          string `json:"id"                    db:"id" dbcast:"uuid"`
	Name        string `json:"name"                  db:"name"`
	Code        string `json:"code"                  db:"code"`
	Description string `json:"description,omitempty" db:"description"`
	Active      bool   `json:"active"                db:"active"`

	// Record timestamps (0050). Written by the touch_row trigger, which fires
	// after the statement's SET list and therefore wins over whatever the
	// reflective UPDATE binds — created_at is immutable, updated_at always
	// moves. dbdefault so an unset value on INSERT takes the column DEFAULT
	// rather than binding '' into a timestamptz, which writes NULL.
	CreatedAt string `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt string `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (v VehicleCategory) GetID() string    { return v.ID }
func (v *VehicleCategory) SetID(id string) { v.ID = id }

// PermitClass is a licence class as the licensing authority writes it. Matched
// against Driver.PermitClass, which is and remains free text.
type PermitClass struct {
	ID          string `json:"id"                    db:"id" dbcast:"uuid"`
	Code        string `json:"code"                  db:"code"`
	Name        string `json:"name"                  db:"name"`
	Description string `json:"description,omitempty" db:"description"`
	Active      bool   `json:"active"                db:"active"`

	// Record timestamps (0050). Written by the touch_row trigger, which fires
	// after the statement's SET list and therefore wins over whatever the
	// reflective UPDATE binds — created_at is immutable, updated_at always
	// moves. dbdefault so an unset value on INSERT takes the column DEFAULT
	// rather than binding '' into a timestamptz, which writes NULL.
	CreatedAt string `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt string `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (p PermitClass) GetID() string    { return p.ID }
func (p *PermitClass) SetID(id string) { p.ID = id }

// PermitAuthorisation is one cell of the matrix: this class may operate this
// category. A pair that is absent is not authorised — but an entirely empty
// matrix means the operator has expressed no opinion, and nothing is refused.
type PermitAuthorisation struct {
	ID            string `json:"id"                 db:"id" dbcast:"uuid"`
	PermitClassID string `json:"permitClassId"      db:"permit_class_id" dbcast:"uuid"`
	CategoryID    string `json:"categoryId"         db:"category_id" dbcast:"uuid"`
	Notes         string `json:"notes,omitempty"    db:"notes"`

	// Record timestamps (0050). Written by the touch_row trigger, which fires
	// after the statement's SET list and therefore wins over whatever the
	// reflective UPDATE binds — created_at is immutable, updated_at always
	// moves. dbdefault so an unset value on INSERT takes the column DEFAULT
	// rather than binding '' into a timestamptz, which writes NULL.
	CreatedAt string `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt string `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (p PermitAuthorisation) GetID() string    { return p.ID }
func (p *PermitAuthorisation) SetID(id string) { p.ID = id }
