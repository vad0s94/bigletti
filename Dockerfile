# Вибір базового образу
FROM golang:1.22.6-alpine AS builder

# Встановлення робочої директорії всередині контейнера
WORKDIR /app

# Копіювання go.mod і go.sum для керування залежностями
COPY go.mod go.sum ./

# Встановлення залежностей
RUN go mod download

# Копіювання решти файлів проекту до робочої директорії
COPY . .

# Збірка Go програми
RUN GOARCH=amd64 go build -o main .
RUN ls -la /app/main  # Перевірка наявності файлу main

# Вибір кінцевого образу
FROM alpine:latest

# Встановлення timezone
RUN apk add --no-cache tzdata

# Копіювання зібраної програми з builder до кінцевого образу
COPY --from=builder /app/main /app/main

# Налаштування змінної середовища для timezone
ENV TZ=Europe/Kiev

# Встановлення робочої директорії
WORKDIR /app

# Команда для запуску програми
CMD ["./main"]