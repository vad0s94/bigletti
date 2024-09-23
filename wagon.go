package main

type Wagon struct {
	ID         string `json:"id"`
	Number     string `json:"number"`
	MockupName string `json:"mockup_name"`
	Seats      []int  `json:"seats"`
}

type WagonWithCompartmentAndSeats struct {
	Wagon       Wagon
	Compartment string
	Seats       []int
}
