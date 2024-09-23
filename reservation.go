package main

type Reservation struct {
	FirstName  string    `json:"first_name"`
	LastName   string    `json:"last_name"`
	Passenger  Passenger `json:"passenger_id"`
	Wagon      Wagon     `json:"wagon_id"`
	SeatNumber int       `json:"seat_number"`
}
