package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-redis/redis/v8"
	tele "gopkg.in/telebot.v3"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var bot *tele.Bot
var ordersQty = 5
var userData = make(map[int64]*UserData)
var wagonTypes = map[string]string{
	"%D0%A11": "1 Клас",
	"%D0%A12": "2 Клас",
	"%D0%A13": "3 Клас",
	"%D0%9F":  "Плацкарт",
	"%D0%9A":  "Євро купе",
	"%D0%9C":  "RIC Купе",
}

var rdb *redis.Client
var ctx = context.Background()

var redisHost = os.Getenv("REDIS_HOST")
var redisPort = os.Getenv("REDIS_PORT")

func main() {

	fmt.Println("Starting bot")
	rdb = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", redisHost, redisPort),
		Password: "",
		DB:       0,
	})

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatal("Can't connect Redis: ", err)
	}

	pref := tele.Settings{
		Token:     os.Getenv("TELEGRAM_TOKEN"),
		Poller:    &tele.LongPoller{Timeout: 10 * time.Second},
		ParseMode: "MarkdownV2",
	}

	rand.Seed(time.Now().UnixNano())

	bot, err = tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
		return
	}

	bot.Handle("/start", func(c tele.Context) error {
		userData[c.Sender().ID] = &UserData{}
		userData[c.Sender().ID].state = StateWaitingForPhoneNumber

		fmt.Sprintf("User %d started the bot", c.Sender().ID)

		menu := &tele.ReplyMarkup{ResizeKeyboard: true}
		shareContactBtn := menu.Contact("Поділитися номером")
		menu.Reply(menu.Row(shareContactBtn))

		c.Send("*Привіт!*\n\n" +
			"Я бот Bigletti і допоможу тобі придбати квитки на дефіцитні маршрути. 🚆\n\n" +
			"Поки я вмію робити бронь на одне євро-купе для 1-4 пасажирів. 🎟\n\n")

		return c.Send(
			"Перш ніж ми почнемо, додай в особистому кабінеті Укрзалізниці пасажирів, на яких будемо бронювати квитки \n\n"+
				" А далі поділись своїм номером телефону для авторизації 📞", menu)
	})

	bot.Handle(tele.OnContact, func(c tele.Context) error {
		var normalizedNumber string
		contact := c.Message().Contact
		phone := contact.PhoneNumber
		if len(phone) > 10 {
			normalizedNumber = "+38" + phone[len(phone)-10:]
		} else {
			normalizedNumber = "+38" + phone
		}

		userData[c.Sender().ID].phone = normalizedNumber

		accessToken, profileId, err := getUserData(c.Sender().ID)
		if err != nil {
			fmt.Println("Error getting user data: ", err)
		}

		fmt.Sprintf("User %d shared their phone number: %s", c.Sender().ID, normalizedNumber)

		if accessToken == "" || profileId == 0 {
			err := sendSms(normalizedNumber)
			if err != nil {
				return c.Send("Виникла помилка під час надсилання SMS: " + err.Error())
			}

			userData[c.Sender().ID].state = StateWaitingForSmsCode
			return c.Send("Введи код з смс")
		}

		userData[c.Sender().ID].accessToken = accessToken
		userData[c.Sender().ID].profileId = profileId

		userData[c.Sender().ID].state = StateWaitingForDepartureStation
		return c.Send("Введи станцію відправлення \\(наприклад Київ\\)")
	})

	bot.Handle(tele.OnText, func(c tele.Context) error {
		switch userData[c.Sender().ID].state {
		case StateWaitingForSmsCode:

			var err error
			accessToken, profileId, err := login(userData[c.Sender().ID].phone, c.Text())
			if err != nil {
				return c.Send("Виникла помилка під час авторизації\\: " + err.Error())
			}

			userData[c.Sender().ID].accessToken = accessToken
			userData[c.Sender().ID].profileId = profileId

			saveUserData(c.Sender().ID, accessToken, profileId)
			c.Send("Авторизація пройшла успішно")

			userData[c.Sender().ID].state = StateWaitingForDepartureStation
			return c.Send("Введи станцію відправлення \\(наприклад Київ\\)")

		case StateWaitingForDepartureStation:
			searchText := c.Text()
			stations, err := searchStation(searchText)
			if err != nil {
				return c.Send("Виникла помилка при пошуку станцій\\: " + err.Error())
			}

			var inlineButtons []tele.InlineButton
			for _, station := range stations {
				btn := tele.InlineButton{
					Unique: station.ID.String(),
					Text:   station.Name,
				}
				inlineButtons = append(inlineButtons, btn)
			}

			var rows [][]tele.InlineButton
			for _, button := range inlineButtons {
				rows = append(rows, []tele.InlineButton{button})
			}

			keyboard := &tele.ReplyMarkup{
				InlineKeyboard: rows,
			}

			return c.Send("Станція відправлення\\:", keyboard)
		case StateWaitingForArrivalStation:
			searchText := c.Text()
			stations, err := searchStation(searchText)
			if err != nil {
				return c.Send("Виникла помилка при пошуку станцій\\: " + err.Error())
			}

			var inlineButtons []tele.InlineButton
			for _, station := range stations {
				btn := tele.InlineButton{
					Unique: station.ID.String(),
					Text:   station.Name,
				}
				inlineButtons = append(inlineButtons, btn)
			}

			var rows [][]tele.InlineButton
			for _, button := range inlineButtons {
				rows = append(rows, []tele.InlineButton{button})
			}

			keyboard := &tele.ReplyMarkup{
				InlineKeyboard: rows,
			}

			return c.Send("Станція прибуття:", keyboard)

		case StateWaitingForDepartureDate:
			userData[c.Sender().ID].departureDate = c.Text()
			userData[c.Sender().ID].state = StateWaitingForTrainNumber

			return c.Send("Вкажи повний номер потягу \\(наприклад 019К\\)")

		case StateWaitingForTrainNumber:
			userData[c.Sender().ID].train = c.Text()
			userData[c.Sender().ID].state = StateWaitingWagonType
			var inlineButtons []tele.InlineButton
			for i, wagonType := range wagonTypes {
				btn := tele.InlineButton{
					Unique: i,
					Text:   wagonType,
				}
				inlineButtons = append(inlineButtons, btn)
			}

			var rows [][]tele.InlineButton
			for _, button := range inlineButtons {
				rows = append(rows, []tele.InlineButton{button})
			}

			keyboard := &tele.ReplyMarkup{
				InlineKeyboard: rows,
			}

			return c.Send("Оберіть тип вагону:", keyboard)
		case StateWaitingForRunDate:
			userData[c.Sender().ID].runDate, err = time.ParseInLocation("2006-01-02 15:04:05", c.Text(), time.FixedZone("Europe/Kiev", 3*3600))

			userData[c.Sender().ID].state = StateWaitingForDiiaVerify
			diiaLink := getDiiaLink(c)
			if err := c.Send(fmt.Sprintf("[Підтверди Дію](%s)", diiaLink)); err != nil {
				return err
			}

			for {
				time.Sleep(5 * time.Second)
				if completed := checkDiia(c); completed {
					break
				}
			}

			c.Send("Дія підтверджена")

			// Якщо перевірка вже пройдена
			return startSearch(c, false)

		default:
			return nil
		}
	})

	bot.Handle(tele.OnCallback, func(c tele.Context) error {
		switch userData[c.Sender().ID].state {
		case StateWaitingForDepartureStation:
			userData[c.Sender().ID].stationFrom = c.Data()
			userData[c.Sender().ID].state = StateWaitingForArrivalStation

			return c.Send("Тепер виберіть станцію призначення")
		case StateWaitingForArrivalStation:
			userData[c.Sender().ID].stationTo = c.Data()
			userData[c.Sender().ID].state = StateWaitingForDepartureDate
			return c.Send("Введіть дату відправлення у форматі РРРР\\-ММ\\-ДД")
		case StateWaitingWagonType:
			userData[c.Sender().ID].wagonType = c.Data()
			passengers, err := getPassengers(c)
			if err != nil {
				return c.Send("Виникла помилка при отриманні пасажирів\\: " + err.Error())
			}

			var inlineButtons []tele.InlineButton
			for _, passenger := range passengers {
				userData[c.Sender().ID].availablePassengers = append(userData[c.Sender().ID].availablePassengers, passenger)
				btn := tele.InlineButton{
					Unique: fmt.Sprintf("select_%d", passenger.ID),
					Text:   fmt.Sprintf("%s %s", passenger.FirstName, passenger.LastName),
				}
				inlineButtons = append(inlineButtons, btn)
			}

			confirmButton := tele.InlineButton{
				Unique: "confirm_selection",
				Text:   "Підтвердити вибір",
			}

			inlineButtons = append(inlineButtons, confirmButton)

			var rows [][]tele.InlineButton
			for _, button := range inlineButtons {
				rows = append(rows, []tele.InlineButton{button})
			}

			keyboard := &tele.ReplyMarkup{
				InlineKeyboard: rows,
			}

			userData[c.Sender().ID].state = StateWaitingPassengerSelection

			return c.Send("Хто поїде?", keyboard)
		case StateWaitingPassengerSelection:
			data := strings.TrimSpace(c.Callback().Data)

			if strings.HasPrefix(data, "select_") {
				passengerID, _ := strconv.Atoi(strings.TrimPrefix(data, "select_"))
				fmt.Println(passengerID)
				togglePassengerSelection(c.Sender().ID, passengerID)
				return c.Respond(&tele.CallbackResponse{Text: "Вибір оновлено", ShowAlert: false}) // Оновлюємо вибір пасажирів
			} else if data == "confirm_selection" {
				// Перехід до наступного стану або виконання необхідних дій
				message := "Ви обрали наступних пасажирів\\:\n"
				for _, passenger := range userData[c.Sender().ID].selectedPassengers {
					message += fmt.Sprintf("%s %s\n", passenger.FirstName, passenger.LastName)
				}

				c.Send(message)

				userData[c.Sender().ID].state = StateWaitingForRunDate
				return c.Send("Коли стартують продажі\\? \\(РРРР\\-ММ\\-ДД ГГ\\:ХХ\\:СС\\)")
			}
		case StateWaitingNeedMoreChoice:
			switch c.Data() {
			case "yes":
				return startSearch(c, true)
			case "no":
				c.Send("Дякую за користування ботом Bigletti\\! 🚆")
			}

		default:
		}

		return nil
	})

	bot.Start()
}

func startSearch(c tele.Context, immediately bool) error {

	fmt.Println("Запуск пошуку")
	if immediately == false {
		c.Send("Розпочну пошук в зазначений час")
		time.Sleep(time.Until(userData[c.Sender().ID].runDate))
	}

	var trip Direct
	var err error
	for i := 0; i < 30; i++ {
		trip, err = getTripId(
			c,
			userData[c.Sender().ID].stationFrom,
			userData[c.Sender().ID].stationTo,
			userData[c.Sender().ID].departureDate,
			userData[c.Sender().ID].train,
		)

		if err != nil {
			break // якщо ID не пустий, виходимо з циклу
		}

		time.Sleep(500 * time.Millisecond)
	}

	wagonWithCompartmentAndSeats, err := getWagonWithCompartmentAndSeats(
		c,
		trip,
		len(userData[c.Sender().ID].selectedPassengers),
		userData[c.Sender().ID].wagonType,
	)
	if err != nil {
		return c.Send("Виникла помилка при пошуку вагону: " + err.Error())
	}

	go func() {
		var message strings.Builder
		message.WriteString("Варіанти бронювання\\: \n")
		for _, wagon := range wagonWithCompartmentAndSeats {
			message.WriteString(fmt.Sprintf("*Вагон:* %s\n*Купе:* %s\n*Місця:* %v\n\n", wagon.Wagon.Number, wagon.Compartment, wagon.Seats))
		}

		// Надсилаємо єдине повідомлення
		c.Send(message.String())
	}()

	makeReservation(c, trip, wagonWithCompartmentAndSeats)

	return nil
}

func needMore(c tele.Context) error {
	userData[c.Sender().ID].state = StateWaitingNeedMoreChoice
	// Створюємо клавіатуру з кнопками "Так" і "Ні"
	markup := &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{
				{Text: "Так", Data: "yes"},
				{Text: "Ні", Data: "no"},
			},
		},
	}

	// Надсилаємо повідомлення з клавіатурою
	c.Send("Потрібно ще?", markup)

	return nil
}

func makeReservation(c tele.Context, trip Direct, wagonWithCompartmentAndSeats []WagonWithCompartmentAndSeats) error {
	allReservations, err := createReservationsForPassengersInCompartments(
		userData[c.Sender().ID].selectedPassengers,
		wagonWithCompartmentAndSeats,
		ordersQty,
	)

	if err != nil {
		return c.Send("Виникла помилка при призначенні місць: " + err.Error())
	}

	for _, reservations := range allReservations {
		go func(reservations []Reservation) {
			cartId, err := makeOrder(c, reservations, trip.ID.String())
			if err != nil {
				// Отримати копію c для кожної горутини
				c.Send("Виникла помилка при створенні замовлення: " + err.Error())
				return
			}

			paymentLink, err := makePaymentLink(c, cartId)
			if err != nil {
				c.Send("Виникла помилка при створенні посилання на оплату: " + err.Error())
				return
			}

			var message strings.Builder
			message.WriteString(fmt.Sprintf("*Замовлення* %s\n", cartId))
			for _, reservation := range reservations {
				message.WriteString(
					fmt.Sprintf(
						"*Пасажир:* %s %s\n*Вагон:* %s\n*Місце:* %d\n ",
						reservation.FirstName,
						reservation.LastName,
						reservation.Wagon.Number,
						reservation.SeatNumber,
					),
				)
			}

			message.WriteString(fmt.Sprintf("[Оплатити](%s)\n", paymentLink))
			c.Send(message.String())
		}(reservations)
	}

	time.Sleep(5 * time.Second)
	needMore(c)

	return nil
}

func sendRequest(method, url string, requestBody []byte, accessToken *string, profileId *int) (json.RawMessage, error) {
	log.Printf("Sending %s request to %s with payload: %s", method, url, string(requestBody))
	req, err := http.NewRequest(method, url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("не вдалося створити запит\\: %v", err)
	}

	var profileIdSuffix string
	if profileId == nil {
		profileIdSuffix = "guest"
	} else {
		profileIdSuffix = strconv.Itoa(*profileId)
	}

	req.Header.Set("x-user-agent", "UZ/2 Web/1 User/"+profileIdSuffix)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if accessToken != nil {
		req.Header.Set("Authorization", "Bearer "+*accessToken)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запит не вдався: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("невдала відповідь сервера: %s", resp.Status)
	}

	var responseBody json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&responseBody); err != nil {
		return nil, fmt.Errorf("не вдалося декодувати відповідь: %v", err)
	}

	// Marshal responseBody back to a JSON string without escaping special characters
	formattedResponse, err := json.MarshalIndent(responseBody, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("не вдалося форматувати відповідь: %v", err)
	}

	log.Printf("Response payload: %s", string(formattedResponse))

	return responseBody, nil
}

func sendSms(phoneNumber string) error {
	url := "https://app.uz.gov.ua/api/auth/send-sms"
	requestBody, err := json.Marshal(map[string]string{
		"phone": phoneNumber,
	})
	if err != nil {
		return fmt.Errorf("не вдалося сформувати запит: %v", err)
	}

	_, err = sendRequest("POST", url, requestBody, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

func login(phoneNumber, code string) (string, int, error) {
	url := "https://app.uz.gov.ua/api/auth/login"
	requestBody, err := json.Marshal(map[string]interface{}{
		"phone": phoneNumber,
		"code":  code,
		"device": map[string]interface{}{
			"name":      "Mac OS X 10_15_7",
			"fcm_token": nil,
		},
	})
	if err != nil {
		return "", 0, fmt.Errorf("не вдалося сформувати запит: %v", err)
	}

	respBody, err := sendRequest("POST", url, requestBody, nil, nil)
	if err != nil {
		return "", 0, err
	}

	var tokenData map[string]interface{}
	if err := json.Unmarshal(respBody, &tokenData); err != nil {
		return "", 0, fmt.Errorf("не вдалося декодувати дані токена: %v", err)
	}

	token, ok := tokenData["token"].(map[string]interface{})
	if !ok {
		return "", 0, fmt.Errorf("не вдалося отримати дані токена з відповіді")
	}

	accessToken := token["access_token"].(string)

	profile, ok := tokenData["profile"].(map[string]interface{})
	if !ok {
		return "", 0, fmt.Errorf("не вдалося отримати дані профілю з відповіді")
	}

	profileID, ok := profile["id"].(float64)
	if !ok {
		return "", 0, fmt.Errorf("не вдалося отримати ID профілю з відповіді")
	}

	return accessToken, int(profileID), nil
}

func searchStation(query string) ([]Station, error) {
	encodedQuery := url.QueryEscape(query)
	url := fmt.Sprintf("https://app.uz.gov.ua/api/stations?search=%s", encodedQuery)
	respBody, err := sendRequest("GET", url, nil, nil, nil)
	if err != nil {
		return nil, err
	}

	// Декодуємо JSON-дані у зріз структур Station
	var stations []Station
	if err := json.Unmarshal(respBody, &stations); err != nil {
		return nil, fmt.Errorf("не вдалося декодувати станції: %v", err)
	}

	return stations, nil
}

func checkDiia(c tele.Context) bool {
	url := "https://app.uz.gov.ua/api/v2/profile/diia-verify/status"

	// Виклик функції sendRequest з передачою accessToken і profileId
	respBody, err := sendRequest(
		"GET",
		url,
		nil,
		&userData[c.Sender().ID].accessToken,
		&userData[c.Sender().ID].profileId,
	)
	if err != nil {
		return false
	}

	// Декодуємо JSON-дані у карту
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return false
	}

	// Перевіряємо, чи є ключ "completed" у відповіді та повертаємо його значення
	completed, ok := response["completed"].(bool)
	if !ok {
		return false
	}

	return completed
}

func getDiiaLink(c tele.Context) string {
	url := "https://app.uz.gov.ua/api/v2/profile/diia-verify"

	// Виклик функції sendRequest з передачою accessToken і profileId
	respBody, err := sendRequest(
		"GET",
		url,
		nil,
		&userData[c.Sender().ID].accessToken,
		&userData[c.Sender().ID].profileId,
	)
	if err != nil {
		return ""
	}

	// Декодуємо JSON-дані у карту
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return ""
	}

	// Перевіряємо, чи є ключ "completed" у відповіді та повертаємо його значення

	return response["link"].(string)
}

func getTripId(c tele.Context, stationFromID, stationToID, departureDate, trainNumber string) (Direct, error) {
	stationFromID = url.QueryEscape(stationFromID)
	stationToID = url.QueryEscape(stationToID)
	departureDate = url.QueryEscape(departureDate)

	url := fmt.Sprintf("https://app.uz.gov.ua/api/v3/trips?station_from_id=%s&station_to_id=%s&date=%s", stationFromID, stationToID, departureDate)
	respBody, err := sendRequest("GET", url, nil,
		&userData[c.Sender().ID].accessToken,
		&userData[c.Sender().ID].profileId)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return Direct{}, err // Return empty Direct and error
	}

	var response struct {
		Direct []Direct `json:"direct"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		log.Printf("Error unmarshalling JSON: %v", err)
		return Direct{}, err // Return empty Direct and error
	}

	for _, direct := range response.Direct {
		fmt.Println("Train Number: " + direct.Train.Number) // For debugging purposes
		if strings.Contains(direct.Train.Number, strings.ToUpper(trainNumber)) {
			return direct, nil // Return the Direct and no error
		}
	}

	return Direct{}, err // Return empty Direct and an error
}

func getWagonWithCompartmentAndSeats(c tele.Context, trip Direct, seats int, wagonType string) ([]WagonWithCompartmentAndSeats, error) {
	wagonType = strings.TrimSpace(wagonType)
	url := fmt.Sprintf("https://app.uz.gov.ua/api/v2/trips/%s/wagons-by-class/%s", trip.ID.String(), wagonType)
	respBody, err := sendRequest("GET", url, nil,
		&userData[c.Sender().ID].accessToken,
		&userData[c.Sender().ID].profileId)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return nil, err // Return nil and error
	}

	var wagons []Wagon
	if err := json.Unmarshal(respBody, &wagons); err != nil {
		return nil, err
	}

	var result []WagonWithCompartmentAndSeats

	wagonTitle := wagonTypes[wagonType]

	for _, wagon := range wagons {
		// Map to store the seats in each compartment
		compartments := make(map[string][]int)

		for _, seat := range wagon.Seats {
			compartmentKey := strconv.Itoa(seat / 10)

			// Determine compartment number (first digit) and seat type
			if wagonTitle == "Євро Купе" {
				if _, exists := compartments[compartmentKey]; !exists {
					compartments[compartmentKey] = []int{}
				}
			} else if wagonTitle == "RIC Купе" {
				// Заповнення мапи місцями
				if seat%2 == 0 {
					compartmentKey += " (парне)"
				} else {
					compartmentKey += " (непарне)"
				}
			}

			compartments[compartmentKey] = append(compartments[compartmentKey], seat)
		}

		// Check each compartment for sufficient seats
		for compartment, seatsInCompartment := range compartments {
			if len(seatsInCompartment) >= seats {
				result = append(result, WagonWithCompartmentAndSeats{
					Wagon:       wagon,
					Compartment: compartment,
					Seats:       seatsInCompartment[:seats], // Return only the required number of seats
				})
			}
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no suitable wagon found") // No wagon found that meets the criteria
	}

	return result, nil
}

func getPassengers(c tele.Context) ([]Passenger, error) {
	url := "https://app.uz.gov.ua/api/v2/passengers"
	respBody, err := sendRequest("GET", url, nil,
		&userData[c.Sender().ID].accessToken,
		&userData[c.Sender().ID].profileId)
	if err != nil {
		return nil, err
	}

	// Декодуємо JSON-дані у зріз структур Station
	var passengers []Passenger
	if err := json.Unmarshal(respBody, &passengers); err != nil {
		return nil, fmt.Errorf("не вдалося декодувати станції: %v", err)
	}

	return passengers, nil
}

func createReservationsForPassengersInCompartments(passengers []Passenger, compartments []WagonWithCompartmentAndSeats, maxCompartments int) ([][]Reservation, error) {
	// Визначаємо максимальну кількість купе, яку можна вибрати
	if len(compartments) < maxCompartments {
		maxCompartments = len(compartments)
	}

	// Вибір випадкових купе
	perm := rand.Perm(len(compartments))
	selectedCompartments := perm[:maxCompartments]

	var allReservations [][]Reservation
	for _, idx := range selectedCompartments {
		compartment := compartments[idx]

		// Перевірка, чи в купе достатньо місць для всіх пасажирів
		if len(compartment.Seats) < len(passengers) {
			continue
		}

		reservations := make([]Reservation, len(passengers))
		for j, passenger := range passengers {
			reservations[j] = Reservation{
				FirstName:  passenger.FirstName,
				LastName:   passenger.LastName,
				Passenger:  passenger,
				Wagon:      compartment.Wagon,
				SeatNumber: compartment.Seats[j],
			}
		}
		allReservations = append(allReservations, reservations)
	}

	if len(allReservations) == 0 {
		return nil, fmt.Errorf("жодне купе не має достатньо місць для всіх пасажирів")
	}

	return allReservations, nil
}

func makeOrder(c tele.Context, reservations []Reservation, tripId string) (string, error) {
	url := "https://app.uz.gov.ua/api/v2/orders"

	// Формуємо масив для "reservations"
	var reservationsList []map[string]interface{}
	for _, reservation := range reservations {
		reservationObject := map[string]interface{}{
			"seat_number":        fmt.Sprintf("%d", reservation.SeatNumber),
			"passenger_id":       reservation.Passenger.ID,
			"wagon_id":           reservation.Wagon.ID,
			"first_name":         reservation.Passenger.FirstName,
			"last_name":          reservation.Passenger.LastName,
			"save_in_passengers": false,
		}

		if reservation.Passenger.Privilege != nil {
			reservationObject["input_type"] = 1
			reservationObject["privilege_id"] = reservation.Passenger.Privilege.ID
			reservationObject["privilege_data"] = map[string]interface{}{
				"birthday": reservation.Passenger.PrivilegeData.Birthday,
			}
		}

		reservationsList = append(reservationsList, reservationObject)
	}

	// Формуємо payload
	payload := map[string]interface{}{
		"trip_id":      tripId,
		"reservations": reservationsList,
	}

	// Перетворюємо payload у JSON
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("не вдалося сформувати запит: %v", err)
	}

	// Відправляємо запит
	respBody, err := sendRequest("POST", url, requestBody,
		&userData[c.Sender().ID].accessToken,
		&userData[c.Sender().ID].profileId)
	if err != nil {
		return "", fmt.Errorf("помилка при надсиланні запиту: %v", err)
	}

	// Логуємо отриману відповідь для налагодження
	log.Printf("Response body: %s", respBody)

	// Обробляємо відповідь
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", fmt.Errorf("не вдалося декодувати відповідь: %v", err)
	}

	// Логуємо весь parsed response
	log.Printf("Parsed response: %+v", response)

	// Отримуємо cart_id
	cartID, ok := response["cart_id"].(float64)
	if !ok {
		return "", fmt.Errorf("відповідь не містить cart_id або має неправильний тип")
	}

	// Конвертуємо cartID в строку
	cartIDStr := fmt.Sprintf("%.0f", cartID)

	return cartIDStr, nil
}

func makePaymentLink(c tele.Context, cartId string) (string, error) {

	url := fmt.Sprintf("https://app.uz.gov.ua/api/v2/carts/%s/payment", cartId)

	respBody, err := sendRequest("POST", url, nil,
		&userData[c.Sender().ID].accessToken,
		&userData[c.Sender().ID].profileId)

	if err != nil {
		return "", err
	}

	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", fmt.Errorf("не вдалося декодувати відповідь: %v", err)
	}

	return response["url"].(string), nil
}

func isSelectedPassenger(userID int64, passengerID int) bool {
	for _, passenger := range userData[userID].selectedPassengers {
		if passenger.ID == passengerID {
			return true
		}
	}
	return false
}

func togglePassengerSelection(userID int64, passengerID int) {
	if userData[userID].selectedPassengers == nil {
		userData[userID].selectedPassengers = []Passenger{}
	}

	if isSelectedPassenger(userID, passengerID) {
		// Видалення пасажира зі списку вибраних
		for i, passenger := range userData[userID].selectedPassengers {
			if passenger.ID == passengerID {
				userData[userID].selectedPassengers = append(userData[userID].selectedPassengers[:i], userData[userID].selectedPassengers[i+1:]...)
				break
			}
		}
	} else {
		for _, passenger := range userData[userID].availablePassengers {
			if passenger.ID == passengerID {
				userData[userID].selectedPassengers = append(userData[userID].selectedPassengers, passenger)
				break
			}
		}
	}
}

func getUserData(userID int64) (string, int, error) {
	accessToken, err := rdb.Get(ctx, fmt.Sprintf("user:%d:accessToken", userID)).Result()
	if err == redis.Nil {
		return "", 0, nil // Дані не знайдено
	} else if err != nil {
		return "", 0, fmt.Errorf("не вдалося отримати accessToken: %v", err)
	}

	profileIdStr, err := rdb.Get(ctx, fmt.Sprintf("user:%d:profileId", userID)).Result()
	if err == redis.Nil {
		return accessToken, 0, nil // Дані не знайдено
	} else if err != nil {
		return "", 0, fmt.Errorf("не вдалося отримати profileId: %v", err)
	}

	profileId, err := strconv.Atoi(profileIdStr)
	if err != nil {
		return "", 0, fmt.Errorf("не вдалося конвертувати profileId: %v", err)
	}

	return accessToken, profileId, nil
}

func saveUserData(userID int64, accessToken string, profileId int) error {
	duration := time.Hour * 24

	err := rdb.Set(ctx, fmt.Sprintf("user:%d:accessToken", userID), accessToken, duration).Err()
	if err != nil {
		return fmt.Errorf("не вдалося зберегти accessToken: %v", err)
	}

	err = rdb.Set(ctx, fmt.Sprintf("user:%d:profileId", userID), profileId, duration).Err()
	if err != nil {
		return fmt.Errorf("не вдалося зберегти profileId: %v", err)
	}

	return nil
}
