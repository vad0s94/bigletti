package main

type Passenger struct {
	ID            int            `json:"id"`
	FirstName     string         `json:"first_name"`
	LastName      string         `json:"last_name"`
	PrivilegeData *PrivilegeData `json:"privilege_data"`
	Privilege     *Privilege     `json:"privilege"`
}

type PrivilegeData struct {
	Birthday string `json:"birthday"`
}

type Privilege struct {
	ID int `json:"id"`
}
