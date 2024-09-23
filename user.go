package main

import "time"

type State int

const (
	StateInitial State = iota
	StateWaitingForPhoneNumber
	StateWaitingForSmsCode
	StateWaitingWagonType
	StateWaitingForDepartureStation
	StateWaitingForArrivalStation
	StateWaitingForDepartureDate
	StateWaitingForTrainNumber
	StateWaitingPassengerSelection
	StateWaitingForDiiaVerify
	StateWaitingForRunDate
	StateWaitingNeedMoreChoice
)

type UserData struct {
	stationFrom         string
	stationTo           string
	departureDate       string
	train               string
	wagonType           string
	runDate             time.Time
	accessToken         string
	profileId           int
	phone               string
	state               State
	availablePassengers []Passenger
	selectedPassengers  []Passenger
}
