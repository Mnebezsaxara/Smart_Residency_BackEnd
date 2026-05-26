package handler

import "time"

type profileRow struct {
	ID                 string    `json:"id"`
	FullName           string    `json:"full_name"`
	Email              string    `json:"email"`
	Phone              string    `json:"phone"`
	IIN                string    `json:"iin"`
	PersonType         string    `json:"person_type"`
	City               string    `json:"city"`
	Street             string    `json:"street"`
	PropertyType       string    `json:"property_type"`
	PropertyNumber     string    `json:"property_number"`
	FullAddress        string    `json:"full_address"`
	Entrance           *int      `json:"entrance"`
	Floor              *int      `json:"floor"`
	Apartment          string    `json:"apartment"`
	Role                string    `json:"role"`
	VerificationStatus  string    `json:"verification_status"`
	ParkingPermitStatus string    `json:"parking_permit_status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (p *profileRow) scanFrom(s interface{ Scan(...any) error }) error {
	return s.Scan(
		&p.ID, &p.FullName, &p.Email, &p.Phone, &p.IIN, &p.PersonType,
		&p.City, &p.Street, &p.PropertyType, &p.PropertyNumber, &p.FullAddress,
		&p.Entrance, &p.Floor, &p.Apartment,
		&p.Role, &p.VerificationStatus, &p.ParkingPermitStatus, &p.CreatedAt, &p.UpdatedAt,
	)
}

const profileCols = `
	id, full_name, email, phone, iin, person_type,
	city, street, property_type, property_number, full_address,
	entrance, floor, apartment,
	role, verification_status, COALESCE(parking_permit_status, ''), created_at, updated_at`
