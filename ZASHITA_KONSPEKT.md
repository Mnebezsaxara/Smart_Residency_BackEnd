# SmartResidency — конспект для защиты диплома

**Автор:** Mansur Akai
**Дата:** 2026-05-15 (обновлён)
**Цель документа:** дать тебе понимание каждого инструмента и решения в проекте на уровне «могу объяснить устно перед комиссией».

> Читай по разделам, не пытайся выучить всё за один проход. Прогони этот файл 3-4 раза. После каждого раздела старайся **своими словами** объяснить вслух — это самый эффективный способ запомнить.

> **Порядок чтения для новичка:** Часть 0 → Часть 1 → ... → Часть 16 → Глоссарий в самом конце. Часть 0 объясняет термины «клиент/сервер/HTTP/JSON/порт» которые потом используются везде.

---

# Часть 0. Фундаментальные термины (читать ПЕРВЫМ)

Эта часть объясняет термины, которые в IT считаются «всем понятно», но на самом деле каждый из них надо знать. Без них дальнейший конспект не уложится в голове.

## 0.1. Клиент и сервер

Когда говорят «клиент-серверная архитектура» — имеют в виду две программы:

- **Сервер** — программа, которая **всё время работает** и ждёт запросов. Например, наш Go-бэк на порту 8080.
- **Клиент** — программа, которая **отправляет запрос** серверу и ждёт ответ. Например, наш Flutter на телефоне.

Один сервер может одновременно обслуживать тысячи клиентов. Один клиент может ходить на много разных серверов.

## 0.2. Что такое порт

**Порт** — это число от 0 до 65535, по которому различаются разные программы на одном компьютере. Можно представить так: IP-адрес = адрес дома, порт = номер квартиры.

- Веб-сайты обычно на порту **80** (HTTP) или **443** (HTTPS)
- Наш Go-бэк слушает порт **8080**
- PostgreSQL — **5432**
- HiveMQ MQTT-TLS — **8883**
- Node-RED — **1880**

Когда говорят `localhost:8080` — это «у меня на компе, программа слушающая порт 8080».

## 0.3. TCP/IP

**TCP/IP** — стек протоколов на котором стоит весь интернет. Главное что нужно знать:

- **IP** — слой адресации (у каждого компа есть IP-адрес типа `192.168.1.5`)
- **TCP** — слой надёжной доставки. Гарантирует что байты дойдут в правильном порядке. Использует «соединение» (handshake → передача → закрытие).

HTTP, MQTT, SSE — все они **поверх TCP**. То есть сначала клиент и сервер устанавливают TCP-соединение, потом по нему обмениваются.

## 0.4. DNS

**DNS (Domain Name System)** — система перевода имён в IP-адреса. Когда ты вводишь `e2279f1bd33640749aebe8fb50417c55.s1.eu.hivemq.cloud` — компьютер сначала спрашивает DNS «какой IP у этого имени» и получает что-то типа `34.243.51.12`. Только потом подключается.

## 0.5. HTTP — как Flutter общается с Go-бэком

**HTTP (HyperText Transfer Protocol)** — текстовый протокол запрос-ответ поверх TCP. Один цикл: клиент шлёт запрос → сервер шлёт ответ → соединение закрывается (или переиспользуется через keep-alive).

### Что есть в HTTP-запросе

```
POST /api/v1/auth/login HTTP/1.1
Host: localhost:8080
Authorization: Bearer eyJhbGc...
Content-Type: application/json

{"email":"akaev@gmail.com","password":"12341234"}
```

Состоит из:

1. **Стартовая строка**: метод + путь + версия
2. **Заголовки**: пары `Key: Value` с метаданными
3. **Пустая строка**
4. **Тело** (body): любые данные, у нас обычно JSON

### Что есть в HTTP-ответе

```
HTTP/1.1 200 OK
Content-Type: application/json

{"token":"eyJhbGc...","user_id":"befabae1...","role":"admin"}
```

1. **Статус-строка**: версия + код + текст
2. **Заголовки**
3. **Тело**

### HTTP-методы (что значит GET, POST, PUT, PATCH, DELETE)

| Метод      | Зачем используется                     | Пример у нас                                                      |
| ---------- | -------------------------------------- | ----------------------------------------------------------------- |
| **GET**    | Прочитать данные. Не меняет состояние. | `GET /api/v1/sensors/events` — получить список событий            |
| **POST**   | Создать новый ресурс.                  | `POST /api/v1/auth/login` — создать сессию (вход)                 |
| **PUT**    | Полностью заменить ресурс.             | `PUT /api/v1/profiles/:id` — обновить весь профиль                |
| **PATCH**  | Частично обновить ресурс.              | `PATCH /admin/sensors/events/:id/status` — поменять только статус |
| **DELETE** | Удалить ресурс.                        | (мы делаем через POST .../delete для FCM-токенов)                 |

### HTTP-коды ответа

| Код                           | Что значит                                                        |
| ----------------------------- | ----------------------------------------------------------------- |
| **200 OK**                    | Запрос успешный, есть тело ответа                                 |
| **201 Created**               | Создано (используем при register)                                 |
| **204 No Content**            | Успех, но без тела (например ответ на CORS-preflight)             |
| **400 Bad Request**           | Клиент прислал плохой запрос (битый JSON, нет обязательного поля) |
| **401 Unauthorized**          | Не авторизован (нет токена или он истёк)                          |
| **403 Forbidden**             | Авторизован, но прав нет (резидент полез в /admin)                |
| **404 Not Found**             | Ресурс не существует (event с таким id нет в БД)                  |
| **409 Conflict**              | Конфликт (email уже зарегистрирован)                              |
| **500 Internal Server Error** | Что-то сломалось у нас на сервере                                 |
| **502 Bad Gateway**           | Сервер не смог достучаться до зависимости (MQTT publish упал)     |
| **503 Service Unavailable**   | Сервис временно недоступен (MQTT не подключён)                    |

Запомни: **2xx = успех**, **4xx = виноват клиент**, **5xx = виноват сервер**.

## 0.6. JSON

**JSON (JavaScript Object Notation)** — формат текстовых данных. Был придуман для JavaScript, но стал стандартом для API.

Пример:

```json
{
  "id": "evt-34",
  "type": "WATER",
  "entrance_num": 2,
  "floor": 5,
  "status": "DETECTED",
  "threat_type": null,
  "timeline": [{ "status": "DETECTED", "at": "2026-05-11T22:52:43" }]
}
```

Типы значений: строка `"abc"`, число `42`, `true`/`false`, `null`, объект `{...}`, массив `[...]`.

Используем везде: тело HTTP-запросов/ответов, MQTT-payload, FCM-data.

## 0.7. REST API

**REST (REpresentational State Transfer)** — стиль построения API, основанный на HTTP. Главные принципы:

1. **Ресурсы — это URL-пути.** Пример: «событие №34» = `/api/v1/sensors/events/evt-34`.
2. **Метод HTTP описывает что делать с ресурсом** (GET — прочитать, POST — создать, и т.д.).
3. **Stateless** — сервер не помнит состояние между запросами. Каждый запрос содержит всю нужную информацию (токен в Authorization-заголовке).
4. **Тело и формат данных — JSON** (по соглашению, не обязательно).

Наш API — REST. Альтернативы: GraphQL, gRPC, SOAP. REST выбран потому что **простой, понятный, поддерживается всем**.

## 0.8. Что такое API вообще

**API (Application Programming Interface)** — набор «правил общения» между двумя программами. Это контракт: «если ты мне пришлёшь вот такой запрос — я отвечу вот так».

Например, у нашего бэка API такой: «POST /auth/login с JSON `{email, password}` → отвечаю JSON `{token, user_id, role}` или ошибкой».

Flutter знает этот контракт и пишет код под него.

## 0.9. CORS

**CORS (Cross-Origin Resource Sharing)** — механизм безопасности браузера. Без него страница `evil.com` могла бы из-под пользователя отправить запросы на `bank.com` и украсть данные.

Браузер по умолчанию **блокирует** запросы с одного домена на другой. Сервер должен явно разрешить через заголовок:

```
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: GET,POST,PUT,PATCH,DELETE,OPTIONS
Access-Control-Allow-Headers: Authorization,Content-Type
```

У нас в `main.go` есть middleware который это добавляет. `*` = разрешено всем (для дипломного проекта норм; в продакшене ставят конкретный домен).

Flutter с эмулятора Android — это не браузер, CORS его не касается. Но если потом сделаешь веб-версию админки — без CORS не работает.

## 0.10. Base64

**Base64** — способ закодировать любые байты (включая бинарные) в обычные ASCII-символы. Используется когда нужно передать бинарные данные через текстовый канал (HTTP-заголовки, JSON, URL).

JWT использует base64 в каждой из трёх частей. Поэтому токен — длинная строка из букв, цифр и `-_.`.

## 0.11. .env файл и секреты

`.env` — обычный текстовый файл рядом с проектом, где хранятся **секреты** (пароли БД, ключи API, JWT-секрет). Формат — `KEY=value` по строке.

Пример нашего `.env`:

```
DATABASE_URL=postgres://postgres:04062006@localhost:5432/smart_residency?sslmode=disable
JWT_SECRET=супер_секретная_строка_никому_не_показывать
HIVEMQ_URL=ssl://e2279f1bd33640749aebe8fb50417c55.s1.eu.hivemq.cloud:8883
HIVEMQ_USERNAME=backendTest
HIVEMQ_PASSWORD=Test1234
HIVEMQ_CLIENT_ID=smartresidency-backend
FIREBASE_CREDENTIALS_PATH=./firebase-credentials.json
```

Почему через файл, а не зашитый в код:

1. **Секреты не попадают в Git.** `.env` добавлен в `.gitignore`.
2. **Разные значения на разных окружениях** (local / staging / production) — один код, разные `.env`.

В Go его читает пакет `godotenv`:

```go
_ = godotenv.Load()              // прочитал .env
os.Getenv("JWT_SECRET")          // достал значение
```

## 0.12. TCP-handshake (рукопожатие)

Когда клиент впервые подключается к серверу по TCP, происходит **3-way handshake**:

1. Клиент → сервер: `SYN` (я хочу подключиться)
2. Сервер → клиент: `SYN-ACK` (ок, готов)
3. Клиент → сервер: `ACK` (поехали)

После этого можно слать данные. Это занимает время (~50-200 мс через интернет), поэтому соединения стараются переиспользовать (keep-alive в HTTP, MQTT держит одно постоянно).

Если ещё используется TLS (HTTPS / MQTT:8883) — дополнительно идёт **TLS-handshake** (~200 мс): обмен сертификатами и согласование ключей шифрования.

---

# Часть 1. Общее представление о проекте

## 1.1. Что такое SmartResidency

SmartResidency — это система «умный жилой комплекс», состоящая из трёх частей, которые общаются между собой:

1. **Мобильное приложение на Flutter** — то что видит житель/админ на телефоне.
2. **Сервер (бэкенд) на Go** — мозг всей системы, держит базу данных, авторизацию, бизнес-логику.
3. **IoT-симулятор датчиков на Node-RED** — изображает реальные датчики воды/дыма в подъездах.

Эти три части соединены **HTTP REST API** (Flutter ↔ Go), **MQTT-протоколом** (Node-RED ↔ Go) и **Firebase Cloud Messaging** (Go → Flutter для пушей).

### Что умеет система

- Регистрация/вход жителей и админа
- Верификация жителя (загрузка документов, ручное одобрение админом)
- Заявки в УК с фотографиями (сломанный лифт и т.д.)
- Пропуски гостей (генерация кодов)
- Логи открытия шлагбаума
- **IoT-часть (центральная фича диплома):** сеть датчиков воды и дыма в подъездах. Если датчик срабатывает — приложение жителя того подъезда получает push-уведомление о тревоге. Админ может подтвердить тревогу или пометить как ложную.

---

# Часть 2. Бэкенд на Go — почему именно Go?

## 2.1. Что такое Go (Golang)

**Go** — это язык программирования, созданный компанией Google в 2009 году. Компилируемый (быстрый как C/C++), но со сборщиком мусора (как Java/Python) — пишешь почти на уровне скриптового языка, а результат бежит как нативная программа.

**Сборщик мусора (garbage collector, GC)** — это автоматический менеджер памяти. В C/C++ программист сам делает `malloc/free` (и часто забывает → утечки памяти). В Go этим занимается рантайм: видит что объект больше не используется — освобождает память.

**Компилируемый** — программа сначала через `go build` превращается в **бинарник** (исполняемый файл, .exe на Windows). Запуск этого файла не требует ничего кроме ОС. В отличие от Python (нужен интерпретатор) и Java (нужна JVM).

## 2.2. Почему Go для нашего проекта (главное что нужно сказать комиссии)

1. **Конкурентность из коробки.** В Go есть «горутины» — это сверхлёгкие потоки. У нас в проекте параллельно работают:
   - HTTP-сервер (отвечает Flutter'у)
   - MQTT-подписчик (слушает датчики)
   - OFFLINE-sweeper (горутина, проверяющая каждые 15 сек кто из датчиков замолчал)
   - SSE-broadcaster (рассылает события админу в реальном времени)

   На Python/Node.js этим бы пришлось плясать с потоками или event-loop. В Go ты просто пишешь `go someFunction()` и оно работает параллельно.

2. **Один бинарник.** Скомпилировал — получил один exe-файл. Не нужно ставить рантайм на сервер, как с Node.js или Python.

3. **Строгая типизация.** Ошибки видны на этапе компиляции, не в проде ночью.

4. **Стандартная библиотека сильная.** HTTP, JSON, криптография, шифрование — всё уже в коробке.

5. **Идеален для серверов с высокой нагрузкой.** Один Go-сервис может держать **десятки тысяч одновременных подключений** (важно для SSE и MQTT). Это работает и для монолита (как у нас), и для микросервисов.

> **Внимание для защиты:** не говори что у нас «микросервисная архитектура» — у нас **монолит** (один Go-сервис делает всё). Это нормально для диплома. Микросервисы — это отдельная архитектура где каждая фича = отдельный сервис.

### Альтернативы которые мы рассматривали

- **Node.js** — отбросили потому что в JS один поток + **event-loop** (асинхронная очередь задач). Хорошо для I/O-bound (много мелких HTTP-запросов), плохо для CPU-bound (тяжёлые вычисления блокируют всех).
- **Python (FastAPI)** — медленнее. **GIL (Global Interpreter Lock)** — фишка Python, при которой только один поток исполняет Python-код одновременно. Настоящая параллельность невозможна без обходных путей (multiprocessing).
- **Java/Spring** — слишком тяжёлый для дипломного проекта, JVM ест память, время старта секунды.

### Что значит CPU-bound vs I/O-bound

- **I/O-bound** — программа в основном **ждёт** (диск, сеть, БД). Пример: REST API, читающий из БД.
- **CPU-bound** — программа **считает** (хэширование, ML, рендеринг). Пример: bcrypt-хэширование пароля.

Наш бэк в основном I/O-bound (большинство запросов = «прочитай из БД, отдай JSON»), но bcrypt при логине — CPU-bound. Go хорошо справляется с обоими.

## 2.3. Какие пакеты (библиотеки) мы используем

Смотри файл `go.mod`. Главные:

| Пакет                                 | Зачем                                                                                                                            |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `github.com/gin-gonic/gin`            | HTTP-фреймворк. Это «движок» который принимает запросы от Flutter и маршрутизирует их в наши хэндлеры. Аналог Express в Node.js. |
| `github.com/jackc/pgx/v5`             | Драйвер PostgreSQL. Через него Go общается с базой.                                                                              |
| `github.com/golang-jwt/jwt/v5`        | Генерация и проверка JWT-токенов (авторизация).                                                                                  |
| `golang.org/x/crypto/bcrypt`          | Хеширование паролей (никогда не храним пароль в чистом виде).                                                                    |
| `github.com/eclipse/paho.mqtt.golang` | MQTT-клиент. Через него Go подписывается на топики HiveMQ.                                                                       |
| `firebase.google.com/go/v4`           | Firebase Admin SDK. Через него отправляем push-уведомления.                                                                      |
| `github.com/joho/godotenv`            | Чтение `.env`-файла с секретами (DB пароль, JWT-секрет и т.д.).                                                                  |
| `github.com/google/uuid`              | Генерация уникальных идентификаторов (UUID).                                                                                     |

## 2.4. Базовые конструкции Go (которые встретишь в коде)

Чтобы понимать наш код, нужно знать 9 базовых конструкций Go:

### Package и import

```go
package handler                          // имя пакета — папка handler/

import (
    "context"                            // стандартная библиотека
    "github.com/gin-gonic/gin"           // внешняя зависимость
    "smartresidency/internal/sse"        // наш внутренний пакет
)
```

Каждый Go-файл начинается с `package <имя>`. Все файлы в одной папке должны иметь одно имя пакета.

### Struct — «структура», аналог класса без методов

```go
type Sensor struct {
    ID          string    `json:"id"`
    Type        string    `json:"type"`
    EntranceNum int       `json:"entrance_num"`
    Floor       int       `json:"floor"`
    Status      string    `json:"status"`
    LastSeenAt  time.Time `json:"last_seen_at"`
}
```

- `Sensor` — имя типа
- Поля с большой буквы — публичные (видны другим пакетам), с маленькой — приватные
- `` `json:"id"` `` — тег для JSON-сериализации: когда struct превращают в JSON, поле `ID` станет `"id"`. Без тега было бы `"ID"`.

### Указатели (`*int`, `*string`)

В Go есть **значения** и **указатели**. Указатель — это адрес значения в памяти.

```go
var x int = 5         // значение
var p *int = &x       // указатель на x (& = «взять адрес»)
fmt.Println(*p)       // 5 (* = «прочитать значение по адресу»)
```

Зачем нужны:

1. **Чтобы передавать большие структуры** без копирования (через `&struct`)
2. **Чтобы поле могло быть `null`** в JSON. Если `ConfirmedAt *time.Time`, то значение может быть nil → в JSON будет `null`. Если был бы `time.Time` — то всегда конкретная дата.

В нашем коде:

```go
type SensorEvent struct {
    CheckingAt     *time.Time `json:"checking_at"`     // может быть null
    ConfirmedBy    *string    `json:"confirmed_by"`    // может быть null
}
```

### Interface — контракт «что я умею»

Это **самая важная концепция Go** после struct.

```go
type EventNotifier interface {
    NotifyEvent(ctx context.Context, eventID string) (sent int, err error)
}
```

Это говорит: «любой тип, у которого есть метод `NotifyEvent(ctx, id) (int, error)`, считается `EventNotifier`».

Зачем нужно:

- В `handler/sensors.go` мы зависим от **интерфейса**, а не от конкретного `*fcm.Sender`. Это значит код будет работать даже если завтра поменяем FCM на SMS-провайдер — главное чтобы у нового был метод `NotifyEvent`.
- Если FCM отключён (нет ключа в .env) — `notifier` будет `nil`, и хэндлер это проверяет: `if h.notifier == nil { ... }`.

### Goroutine — лёгкий поток

```go
go someFunc()         // запустить someFunc в фоне, не ждать
go func() {           // или анонимная функция
    doSomething()
}()
```

Главное:

- **Не «настоящий» поток ОС.** Go runtime сам распределяет тысячи горутин по нескольким OS-потокам.
- **Очень лёгкие.** Можно создать 100 000 горутин без проблем (в Java/C# это нереально).
- **Не блокируют main().** Если main завершится — все горутины оборвутся.

У нас горутины запускают:

- `go sub.notifier.NotifyEvent(...)` — отправить пуш не блокируя MQTT-обработку
- `go sensors.NewOfflineSweeper(...).Run(ctx)` — бесконечный sweeper в фоне
- Каждое HTTP-соединение Gin сам обрабатывает в отдельной горутине

### Channel (`chan`) — труба для обмена между горутинами

```go
ch := make(chan []byte, 16)   // буферизованный канал на 16 элементов

// Одна горутина отправляет:
ch <- frame                    // если буфер не полон — кладёт, иначе блокируется

// Другая получает:
data := <-ch                   // ждёт пока придёт что-то
```

У нас в `internal/sse/hub.go` каждый подключённый админ имеет свой канал. Когда происходит событие — Broadcast отправляет фрейм в **каждый** канал. SSE-handler в отдельной горутине читает из своего канала и пишет в HTTP-ответ.

### Defer — отложенный вызов

```go
func myHandler() {
    pool := connectDB()
    defer pool.Close()        // выполнится при выходе из функции
    // ... любой код ...
    return                    // тут вызовется pool.Close()
}
```

Гарантирует что закрытие соединения / разблокировка мьютекса произойдут даже если функция выйдет с ошибкой посередине. Аналог `try-finally` в Java.

### Context (`context.Context`)

`ctx` — это объект, который несёт через всю цепочку вызовов:

1. **Сигнал отмены** — если запрос отменён (клиент закрыл соединение), все вызванные функции узнают через `<-ctx.Done()` и могут остановить работу
2. **Deadline** — «не дольше X секунд»
3. **Значения** — например в Gin через ctx прокидываются `user_id`, `user_role` после middleware

В каждом нашем хэндлере:

```go
ctx := c.Request.Context()       // взяли контекст HTTP-запроса
h.db.QueryRow(ctx, `SELECT ...`) // если запрос отменили — pgx прервёт SQL
```

### Error handling — Go-стиль обработки ошибок

В Go нет `try/catch`. Ошибка — это **второй возвращаемый параметр** функции:

```go
result, err := someFunc()
if err != nil {
    // обработать
    return
}
// использовать result
```

Это многословно, зато каждая ошибка обрабатывается явно. Никаких «забыл поймать exception».

---

# Часть 3. База данных — SQL и PostgreSQL

## 3.1. Что такое SQL

**SQL (Structured Query Language)** — это язык запросов к **реляционным базам данных**. Реляционная — значит данные хранятся в **таблицах** со столбцами и строками, как в Excel, и таблицы связаны между собой по ключам.

Пример SQL-запроса:

```sql
SELECT email, role FROM profiles WHERE entrance = 2 AND verification_status = 'approved';
```

Это значит: «выбери поля email и role из таблицы profiles, где подъезд = 2 и статус верификации = approved».

## 3.2. Что такое PostgreSQL

**PostgreSQL** (он же Postgres) — это конкретная база данных, программа, которая хранит таблицы и понимает SQL-запросы. Бесплатная, опенсорсная, промышленного уровня. Альтернативы: MySQL, SQLite, Oracle.

**Почему именно PostgreSQL:**

- Поддерживает **UUID** (уникальные ID размером 128 бит — никогда не повторяются, нам нужно для user_id)
- Поддерживает **JSONB** (можно хранить JSON прямо в колонке)
- Имеет **FK constraints** (внешние ключи — связи между таблицами с автоматической проверкой целостности)
- Бесплатна

## 3.3. Что такое «схема БД»

**Схема** — это структура базы: какие таблицы, какие у них столбцы, какие связи. У нас файл `database/schema.sql` — это снимок схемы (дамп). А **миграции** — это шаги, которыми мы достраивали схему по мере развития проекта (папка `migrations/`).

## 3.4. Наши таблицы

Смотри `migrations/001_init.sql` — создаются 8 базовых таблиц. После — 002, 003, 004 добавляют новое.

### users — учётные записи

```sql
CREATE TABLE users (
    id            UUID PRIMARY KEY,
    email         VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);
```

Только логин и пароль (захешированный). Минимум.

### profiles — данные пользователя

```sql
CREATE TABLE profiles (
    id                  UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    full_name, email, phone, iin, person_type, ...
    entrance            INT,        -- подъезд
    floor               INT,
    apartment           VARCHAR(50),
    role                VARCHAR(50) DEFAULT 'resident',
    verification_status VARCHAR(50) DEFAULT 'not_submitted'
);
```

- `REFERENCES users(id) ON DELETE CASCADE` — это **внешний ключ**: если удалить пользователя, его профиль удалится автоматически.
- `role` — `resident` или `admin`.
- `verification_status` — `not_submitted` / `pending` / `approved` / `rejected`.
- `entrance/floor/apartment` — нужны для адресной FCM-доставки (пушим только жителям нужного подъезда).

### sensors — реестр датчиков

```sql
CREATE TABLE sensors (
    id           TEXT PRIMARY KEY,        -- 'w-2-5', 's-3-1'
    type         TEXT NOT NULL,           -- 'WATER' / 'SMOKE'
    entrance_num INT  NOT NULL,
    floor        INT  NOT NULL,
    status       TEXT NOT NULL DEFAULT 'NORMAL',  -- NORMAL / ALERT / OFFLINE
    last_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()  -- добавлено в миграции 004
);
```

**54 строки = 3 подъезда × 9 этажей × 2 типа (вода+дым).**

### sensor_events — события тревоги

```sql
CREATE TABLE sensor_events (
    id               TEXT PRIMARY KEY,         -- 'evt-34'
    sensor_id        TEXT NOT NULL REFERENCES sensors(id),
    type, entrance_num, floor,
    status           TEXT DEFAULT 'DETECTED',  -- DETECTED → CHECKING → CONFIRMED / FALSE_ALARM
    threat_type      TEXT,                     -- 'WATER_LEAK' / 'FIRE'
    admin_comment    TEXT,
    created_at       TIMESTAMPTZ DEFAULT NOW(),
    checking_at      TIMESTAMPTZ,
    confirmed_at     TIMESTAMPTZ,
    false_alarmed_at TIMESTAMPTZ,
    confirmed_by     UUID REFERENCES users(id)
);
```

- Каждое срабатывание датчика создаёт **отдельное событие**.
- Из 4 timestamps + confirmed_by мы строим **timeline** для UI.

### fcm_tokens — push-токены устройств

```sql
CREATE TABLE fcm_tokens (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT PRIMARY KEY,
    platform   TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Когда Flutter логинится, он получает от Firebase свой FCM-токен и шлёт нам через `POST /users/me/fcm-token`. Один юзер может иметь несколько токенов (с разных устройств).

### Остальные таблицы:

- `service_requests` + `request_photos` — заявки в УК и фото к ним
- `guest_access` — гостевые пропуски
- `barrier_logs` — журнал открытия шлагбаума
- `verification_requests` + `verification_documents` — заявки на верификацию резидента

## 3.5. SQL-команды которые мы используем (с примерами из нашего кода)

### CREATE — создать таблицу/индекс

```sql
CREATE TABLE sensors (id TEXT PRIMARY KEY, ...);
CREATE INDEX idx_sensors_entrance ON sensors(entrance_num);
```

### SELECT — прочитать

```sql
SELECT entrance_num, floor, COUNT(*) c
FROM sensor_events
WHERE created_at >= $1
GROUP BY entrance_num, floor
ORDER BY c DESC LIMIT 5;
```

- `SELECT ... FROM` — что и откуда читать
- `WHERE` — фильтр
- `GROUP BY` — группировка
- `ORDER BY ... DESC` — сортировка по убыванию
- `LIMIT 5` — максимум 5 строк

### INSERT — добавить строку

```sql
INSERT INTO sensor_events (id, sensor_id, type, ...)
VALUES ('evt-' || nextval('sensor_events_seq'), $1, $2, ...)
RETURNING id;
```

`RETURNING id` — отдай мне обратно сгенерированный id (полезно когда `id` создаёт сама БД).

### UPDATE — изменить строки

```sql
UPDATE sensors
SET status='OFFLINE', last_updated=NOW()
WHERE last_seen_at < NOW() - make_interval(secs => $1);
```

### DELETE — удалить

```sql
DELETE FROM fcm_tokens WHERE token = $1 AND user_id = $2;
```

Второе условие `AND user_id = $2` — защита, чтобы один юзер не удалил чужой токен.

### UPSERT (INSERT … ON CONFLICT) — вставить или обновить

Это **очень важная конструкция** PostgreSQL.

```sql
INSERT INTO sensors (id, status, last_seen_at)
VALUES ($1, $2, NOW())
ON CONFLICT (id) DO UPDATE
SET status = EXCLUDED.status, last_seen_at = NOW();
```

- Если такой `id` уже есть — обновить
- Если нет — вставить
- `EXCLUDED.status` = значение которое мы пытались вставить

Без этого нам пришлось бы делать SELECT, потом INSERT или UPDATE в зависимости от результата — **2 запроса и race condition**. UPSERT — один атомарный запрос.

### JOIN — связать две таблицы

Используем в `fcm/sender.go`:

```sql
SELECT t.token
FROM fcm_tokens t
JOIN profiles p ON p.id = t.user_id
WHERE p.entrance = $1 AND p.verification_status = 'approved';
```

Это значит: «возьми токен из `fcm_tokens`, но **только для тех юзеров**, у которых в `profiles` подъезд = $1 и статус = approved».

JOIN буквально склеивает строки двух таблиц по условию (у нас по user_id). Без JOIN пришлось бы делать два запроса.

Виды JOIN: `INNER JOIN` (по умолчанию — только совпадения), `LEFT JOIN` (всё из левой + совпадения из правой), `RIGHT JOIN`, `FULL OUTER JOIN`.

### ALTER TABLE — менять схему таблицы

Используем во всех миграциях:

```sql
ALTER TABLE sensors ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
```

`IF NOT EXISTS` — идемпотентность: можно прогонять много раз, ошибки не будет.

## 3.6. Ключи и индексы (что эти странные слова значат)

### PRIMARY KEY (первичный ключ)

Уникальный идентификатор строки. **Не может быть NULL**, **не может повторяться**.

```sql
id UUID PRIMARY KEY
```

На него БД автоматически создаёт индекс.

### FOREIGN KEY (внешний ключ, FK)

Ссылка на строку в **другой таблице**. БД проверяет что ссылка валидна.

```sql
user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE
```

- `REFERENCES users(id)` — должно быть значение, существующее в users.id
- `ON DELETE CASCADE` — если в users эту запись удалят, эту тоже удалить автоматически

Это **гарантия целостности данных**. Невозможно создать orphan-запись «событие принадлежит несуществующему датчику».

### UNIQUE

Значение не может повторяться, но **может быть NULL** (один или несколько раз — зависит от БД).

```sql
email VARCHAR(255) UNIQUE NOT NULL
```

### INDEX

**Структура для быстрого поиска**. По умолчанию БД при `WHERE email = '...'` читает всю таблицу подряд (O(n)). Если есть индекс на колонку — поиск O(log n) (как B-дерево).

```sql
CREATE INDEX idx_sensor_events_entrance ON sensor_events(entrance_num);
```

Теперь `SELECT * FROM sensor_events WHERE entrance_num = 2` будет быстрым даже на миллионе строк.

**Минусы индексов:** занимают место + замедляют INSERT/UPDATE (БД должна обновлять индекс).

Правило: индекс на колонку, которая часто фигурирует в `WHERE`. У нас в `migrations/002_sensors.sql`:

```sql
CREATE INDEX idx_sensors_entrance ON sensors(entrance_num);
CREATE INDEX idx_sensor_events_created  ON sensor_events(created_at DESC);
CREATE INDEX idx_sensor_events_status   ON sensor_events(status);
```

## 3.7. UUID — почему не обычный auto-increment ID

**UUID (Universally Unique Identifier)** — 128-битный идентификатор вроде `befabae1-503c-4c8b-b9e9-4b1dde3217aa`.

Почему UUID а не int id (1, 2, 3...)?

1. **Безопасность**: по `/users/3` злоумышленник может пытаться перебирать `/users/4`, `/users/5`. С UUID — нереально угадать.
2. **Не выдаём бизнес-инфу**: если в БД 1000 юзеров, обычные id выдадут это (`/users/999`). UUID — нет.
3. **Distributed-friendly**: несколько серверов могут генерировать UUID независимо, не боясь конфликтов.

Минус: занимает 16 байт (vs 4 байта для int). Для современных БД — несущественно.

## 3.8. Транзакции и ACID

**Транзакция** — несколько SQL-запросов, которые выполняются как одна атомарная единица. Либо **все вместе** успешно, либо **никакого** изменения.

Пример где бы пригодилось: при подтверждении event'а — обновить sensor_events + создать запись в audit_log. Если второе упадёт — первое должно откатиться.

В нашем проекте мы транзакции **почти не используем** (для диплома сойдёт). Но если спросят — знай **ACID**:

- **A**tomicity — всё или ничего
- **C**onsistency — БД переходит из одного валидного состояния в другое
- **I**solation — параллельные транзакции не мешают друг другу
- **D**urability — после COMMIT данные не пропадут даже при отказе питания

В Go это делается так:

```go
tx, _ := pool.Begin(ctx)
defer tx.Rollback(ctx)
tx.Exec(ctx, "UPDATE ...")
tx.Exec(ctx, "INSERT ...")
tx.Commit(ctx)
```

## 3.9. Connection pool — что такое pgxpool

**Pool (пул) соединений** — это набор готовых TCP-соединений с БД, которые переиспользуются.

Открыть новое соединение с PostgreSQL = TCP-handshake + аутентификация = ~50 мс. Если делать на каждый запрос — сервер захлебнётся.

Pool держит, например, 10 уже открытых соединений в памяти. Когда нужно сделать SELECT — берём свободное соединение из пула, делаем, возвращаем. Молниеносно.

У нас:

```go
pool, _ := pgxpool.New(ctx, dsn)
// ... теперь все запросы через pool.QueryRow / pool.Exec
```

---

# Часть 4. Дерево проекта — что где лежит

```
SmartRes/
├── cmd/
│   └── server/
│       └── main.go              ← точка входа, тут стартует сервер
│
├── internal/                    ← вся бизнес-логика (internal = недоступно извне)
│   ├── db/
│   │   └── db.go               ← подключение к PostgreSQL
│   ├── middleware/
│   │   └── auth.go             ← проверка JWT-токенов
│   ├── handler/                ← HTTP-обработчики (по одному файлу на тему)
│   │   ├── auth.go             ← /register, /login, /me, /refresh
│   │   ├── profiles.go         ← /profiles/:id
│   │   ├── service_requests.go ← заявки в УК (с assignment на staff)
│   │   ├── staff.go            ← /admin/staff — список сотрудников + нагрузка
│   │   ├── guests.go           ← гостевые пропуска
│   │   ├── barrier_v2.go       ← шлагбаум v2: сканирование номера, MQTT-команда
│   │   ├── admin_barrier.go    ← /admin/barrier — ручное управление шлагбаумом
│   │   ├── vehicles.go         ← регистрация авто жителей
│   │   ├── parking.go          ← парковочные места и бронирования
│   │   ├── parking_permit.go   ← парковочные пропуска для гостей
│   │   ├── notifications.go    ← in-app уведомления в приложении
│   │   ├── log.go              ← барьер-логи
│   │   ├── verification.go     ← верификация жителя
│   │   ├── sensors.go          ← датчики (главная IoT-фича)
│   │   ├── fcm.go              ← регистрация/удаление FCM-токенов
│   │   └── types.go            ← общие типы (profileRow и т.д.)
│   ├── mqtt/
│   │   └── client.go           ← MQTT-подписчик + публикатор
│   ├── fcm/
│   │   └── sender.go           ← отправка push через Firebase
│   ├── sensors/
│   │   ├── seed.go             ← заполнение реестра 54 датчиков
│   │   └── sweeper.go          ← OFFLINE-детектор (горутина)
│   └── sse/
│       └── hub.go              ← Server-Sent Events рассылка
│
├── migrations/                  ← SQL-миграции по порядку
│   ├── 001_init.sql            ← базовые 8 таблиц
│   ├── 002_sensors.sql         ← sensors, sensor_events, fcm_tokens
│   ├── 003_verification_address.sql  ← entrance/floor в verification_requests
│   ├── 004_barrier_vehicles.sql      ← vehicles + barrier_events таблицы
│   ├── 005_parking.sql         ← parking_spots, parking_bookings, parking_events
│   ├── 006_parking_permit.sql  ← parking_permits таблица
│   ├── 007_parking_permit_v2.sql     ← доп. поля parking_permits
│   ├── 008_notifications.sql   ← notifications таблица (in-app)
│   ├── 009_adjust_parking_spots.sql  ← корректировка типов парковки
│   ├── 010_barrier_gate_id.sql ← поле gate_id для multi-gate
│   ├── 011_heartbeat_offline.sql     ← last_seen_at, timeline-колонки датчиков
│   └── 012_staff_roles.sql     ← specialty в profiles, assigned_to + taken_at в service_requests
│
├── database/
│   └── schema.sql              ← снимок текущей схемы (для бэкапа)
│
├── uploads/                     ← загруженные файлы (фото, документы)
│
├── nodered_barrier_flow.json   ← Node-RED flow для симуляции шлагбаума (MQTT-команды)
├── nodered_parking_flow.json   ← Node-RED flow для симуляции парковочных датчиков
├── flows.json                  ← основной Node-RED flow (датчики воды/дыма, heartbeat)
├── firebase-credentials.json   ← секрет от Firebase (для FCM)
├── .env                        ← секреты (DB пароль, JWT-секрет, HiveMQ пароль)
├── go.mod / go.sum             ← список зависимостей Go
└── ZASHITA_KONSPEKT.md         ← этот файл
```

### Зачем такое разделение

Стандартный Go-layout:

- `cmd/` — все исполняемые программы (у нас только `server`, но могло быть несколько)
- `internal/` — приватный код, который не может импортироваться чужими проектами
- Внутри `internal/` каждый пакет = отдельная область ответственности. **Принцип single responsibility.**

---

# Часть 5. Точка входа — `cmd/server/main.go`

Это файл, который запускается командой `go run ./cmd/server`. Что он делает по порядку:

```go
func main() {
    _ = godotenv.Load()                    // 1. Читает .env

    pool, err := db.Connect(...)           // 2. Подключается к PostgreSQL
    defer pool.Close()

    sensors.Seed(ctx, pool)                // 3. Создаёт 54 датчика если их нет

    // 4. Инициализирует FCM (Firebase) если есть ключ
    s, _ := fcm.New(ctx, credPath, pool)

    hub := sse.NewHub()                    // 5. Создаёт SSE-хаб

    // 6. Стартует горутину OFFLINE-sweeper
    go sensors.NewOfflineSweeper(pool, s, hub).Run(ctx)

    // 7. Подключается к MQTT-брокеру HiveMQ
    sub, _ := mqtt.New(cfg, pool, s, hub)
    defer sub.Close()

    r := gin.Default()                     // 8. Создаёт HTTP-роутер
    // CORS, статика, регистрация всех эндпоинтов...
    r.Run(":8080")                         // 9. Запускает HTTP-сервер
}
```

**Главное что нужно понять:** все компоненты запускаются параллельно в одной программе. HTTP-сервер обслуживает Flutter, MQTT-подписчик в фоне ловит датчики, sweeper в фоне проверяет таймауты. Это всё **горутины** — лёгкие потоки в Go.

---

# Часть 6. Аутентификация — JWT и bcrypt

## 6.1. Что такое JWT

**JWT (JSON Web Token)** — стандарт токена, который выглядит как 3 base64-куска через точки: `header.payload.signature`.

Пример нашего токена:

```
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiYmVmYWJhZTEt...eyJyb2xlIjoiYWRtaW4ifQ.FfiKqiN2dbT...
```

- **header** — какой алгоритм подписи (у нас HS256)
- **payload** — данные: `user_id`, `email`, `role`, `exp` (срок действия), `iat` (когда выдан)
- **signature** — HMAC-SHA256 от header+payload, ключ — наш JWT_SECRET из `.env`

**Главная фишка JWT:** сервер **не хранит** токен. Сервер только проверяет подпись. Если подпись валидна и срок не истёк — токен принят. Это удобно для горизонтального масштабирования (несколько копий сервера за балансировщиком).

### Как создаём токен (`handler/auth.go` функция `makeToken`):

```go
claims := middleware.Claims{
    UserID: userID,
    Email:  email,
    Role:   role,
    RegisteredClaims: jwt.RegisteredClaims{
        ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
        IssuedAt:  jwt.NewNumericDate(time.Now()),
    },
}
t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
return t.SignedString([]byte(os.Getenv("JWT_SECRET")))
```

Срок жизни — 24 часа. Алгоритм — HS256 (симметричная подпись).

### Как проверяем токен (`middleware/auth.go`):

```go
func Auth(secret string) gin.HandlerFunc {
    return func(c *gin.Context) {
        raw := ""
        if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
            raw = strings.TrimPrefix(h, "Bearer ")
        } else if q := c.Query("token"); q != "" {
            raw = q   // для SSE/EventSource
        }
        token, err := jwt.ParseWithClaims(raw, &Claims{}, ...)
        // если ок — кладём user_id, role в контекст
        c.Set("user_id", claims.UserID)
        c.Set("user_role", claims.Role)
        c.Next()
    }
}
```

Middleware (промежуточный слой) — выполняется на КАЖДОМ приватном запросе. Если токен битый — 401 Unauthorized.

## 6.2. Что такое bcrypt

**bcrypt** — алгоритм хеширования паролей. Особенности:

- Медленный (намеренно — чтобы перебор по словарю был нереальным)
- Включает «соль» автоматически (защита от радужных таблиц)

```go
hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
// Сохраняем hash в БД

// При логине:
bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password))
```

**Никогда** не храним сам пароль. Только хеш. Даже если базу украдут — пароли не восстановят.

## 6.3. Hashing vs encryption (важная разница для комиссии)

Это **две принципиально разные вещи**, путать нельзя:

|                 | Hashing (хеширование)                            | Encryption (шифрование)                |
| --------------- | ------------------------------------------------ | -------------------------------------- |
| **Обратимость** | Необратимое: из хеша нельзя получить исходник    | Обратимое: с ключом можно расшифровать |
| **Зачем**       | Проверить «это то же значение?», но не вытащить  | Передать секретный текст безопасно     |
| **Пример**      | bcrypt для паролей, SHA-256 для контрольных сумм | AES, RSA, TLS                          |
| **У нас**       | bcrypt для паролей                               | TLS для MQTT, HTTPS, JWT-подпись HMAC  |

Пароль **хешируется** потому что нам никогда не нужен исходник — нам нужно только проверить «то же ли это значение что юзер ввёл при регистрации».

## 6.4. Что такое HMAC и почему именно HS256 для JWT

**HMAC (Hash-based Message Authentication Code)** — алгоритм проверки целостности и подлинности данных через хеш + общий секрет.

Идея: чтобы доказать что сообщение пришло от того кто знает секрет, мы делаем `HMAC(сообщение, секрет)` и добавляем к сообщению. Получатель, зная тот же секрет, вычисляет HMAC сам и сравнивает. Без секрета подделать невозможно.

**HS256** = HMAC + SHA-256 (хеш-функция). Это **симметричный** алгоритм — один и тот же секрет используется для подписи и проверки.

Альтернатива — **RS256** (RSA-подпись): пара ключей (приватный для подписи, публичный для проверки). Нужно когда подписывает один сервис, а проверяют много (например, OAuth-серверы).

У нас один Go-бэк и подписывает, и проверяет — поэтому HS256 + один секрет в .env подходит идеально.

## 6.5. Зачем `exp` (срок жизни) и что такое refresh token

JWT **нельзя отозвать**. Сервер только проверяет подпись и срок. Если токен украли — кража действительна до `exp`.

Поэтому:

- **Короткий срок** (у нас 24 часа, в продакшене обычно 15-60 минут)
- **Refresh-эндпоинт** (`POST /auth/refresh`) — позволяет получить новый токен по старому

В нашем проекте refresh-токен по факту не используется (24 часа достаточно). В продакшене нормальный подход:

1. **Access token** — 15 минут, идёт в каждый запрос
2. **Refresh token** — 30 дней, хранится отдельно, используется только чтобы получить новый access token

---

# Часть 7. HTTP API — Gin фреймворк

## 7.1. Что такое Gin

**Gin** — HTTP-фреймворк для Go. Берёт на себя:

- Парсинг URL (`/sensors/events/:id` → `id = "evt-34"`)
- JSON-сериализацию (response.JSON автоматически)
- Middleware-цепочки (auth → handler → log)
- CORS (разрешения для браузера)

## 7.2. Как зарегистрирован эндпоинт (пример из main.go)

```go
priv := api.Group("/")
priv.Use(middleware.Auth(secret))   // вешаем JWT-проверку на всю группу
{
    sensorH := handler.NewSensorHandler(pool, notifier, publisher, hub)
    priv.GET("/sensors/events/:id", sensorH.GetEventDetail)
    priv.POST("/admin/sensors/:id/reset", sensorH.Reset)
    // ...
}
```

`priv.Use(middleware.Auth(secret))` означает: перед каждым обработчиком этой группы Gin сначала прогонит auth-middleware. Если токен невалидный — handler даже не вызовется.

## 7.3. Пример полного handler'а (`handler/sensors.go` — Reset)

```go
func (h *SensorHandler) Reset(c *gin.Context) {
    // 1. Проверка роли (резидент сюда не пройдёт)
    if c.GetString("user_role") != "admin" {
        c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
        return
    }
    // 2. Проверка что MQTT-publisher настроен
    if h.publisher == nil {
        c.JSON(http.StatusServiceUnavailable, ...)
        return
    }
    // 3. Достаём ID из URL
    id := c.Param("id")

    // 4. Грузим датчик из БД
    var s Sensor
    err := h.db.QueryRow(ctx, `SELECT ... FROM sensors WHERE id=$1`, id).Scan(...)
    if errors.Is(err, pgx.ErrNoRows) {
        c.JSON(http.StatusNotFound, gin.H{"error": "sensor not found"})
        return
    }

    // 5. Публикуем MQTT-сообщение со status=NORMAL
    topic := fmt.Sprintf("smartresidency/sensors/%d/%d/%s", s.EntranceNum, s.Floor, s.Type)
    payload := map[string]any{
        "id": s.ID, "type": s.Type, ..., "status": "NORMAL",
    }
    h.publisher.Publish(topic, payload)

    // 6. Отвечаем клиенту
    c.JSON(http.StatusOK, gin.H{"ok": true, "topic": topic})
}
```

---

# Часть 8. IoT-архитектура — Node-RED, MQTT, HiveMQ

Это **сердце диплома.** Тут самые важные понятия.

## 8.1. Что такое IoT

**IoT (Internet of Things)** — Интернет вещей. Концепция, при которой физические устройства (датчики, лампочки, замки) подключены к сети и обмениваются данными между собой и серверами.

Пример в нашем подъезде:

- Датчик воды на полу 5 этажа замечает влагу → шлёт сигнал
- Датчик дыма в коридоре 3 этажа замечает дым → шлёт сигнал
- Сервер получает сигнал → шлёт push жителям

**Главный вопрос IoT:** как 54 устройства одновременно общаются с сервером эффективно? HTTP с REST API для этого плохо подходит — мы решили задачу через **MQTT**.

## 8.2. Что такое MQTT

**MQTT (Message Queuing Telemetry Transport)** — лёгкий протокол сообщений по модели **publish/subscribe (pub/sub)**, специально созданный для IoT (придуман IBM в 1999 для нефтепроводов в пустыне).

### Ключевые понятия MQTT

**Брокер (broker)** — посредник. Все клиенты подключаются к нему, и он раздаёт сообщения. У нас брокер — **HiveMQ Cloud**.

**Издатель (publisher)** — клиент который шлёт сообщения. У нас это Node-RED (датчики) и наш Go-бэк (когда делаем reset).

**Подписчик (subscriber)** — клиент который слушает сообщения. У нас это наш Go-бэк (он подписан на все sensor-топики).

**Топик (topic)** — иерархический путь, по которому фильтруются сообщения. Пример: `smartresidency/sensors/2/5/WATER` — водяной датчик 2 подъезда 5 этажа. Уровни разделяются `/`.

**Wildcard'ы в подписке:**

- `+` — один уровень (`sensors/+/+/+` = все датчики любых подъездов и этажей)
- `#` — много уровней (`sensors/#` = вся ветка sensors)

**QoS (Quality of Service)** — гарантии доставки:

- QoS 0: «at most once» — fire and forget, может потеряться
- QoS 1: «at least once» — гарантированная доставка, но возможен дубль (мы используем это)
- QoS 2: «exactly once» — точно один раз, но самый медленный

**Retained message** — сообщение остаётся на брокере. Когда новый подписчик подключается — он сразу получает последнее retained-сообщение по топику. У нас этим пользуется reset-эндпоинт: публикуем с `retain=true`, и если бэк перезапустится — он сразу узнает текущий статус.

### Почему MQTT а не HTTP

| Параметр                        | MQTT                        | HTTP                                |
| ------------------------------- | --------------------------- | ----------------------------------- |
| Размер заголовка                | 2 байта                     | 200+ байт                           |
| Соединение                      | Долгоживущее (один раз TCP) | Каждый запрос — новый TCP-handshake |
| Push с сервера к клиенту        | Да (out-of-the-box)         | Нет (нужен SSE/WebSocket)           |
| Энергопотребление на устройстве | Минимум                     | Высокое                             |

Для 54 датчиков, шлющих heartbeat каждые 30 сек, MQTT кратно эффективнее.

## 8.3. Что такое HiveMQ

**HiveMQ** — готовый облачный MQTT-брокер. У нас бесплатный тариф (HiveMQ Cloud), URL:

```
e2279f1bd33640749aebe8fb50417c55.s1.eu.hivemq.cloud:8883
```

- Порт `8883` — стандартный MQTT-over-TLS (`8883`= secure, `1883`= plaintext).
- TLS — шифрование канала, чтобы кто-то по пути не мог прочитать или подменить сообщения.
- Аутентификация — логин/пароль (`backendTest` / `Test1234`).

### Альтернативы

- Самим поднять Mosquitto / EMQX / HiveMQ Community Edition в Docker. Мы не стали — это дополнительная инфраструктура, плюс HiveMQ Cloud даёт нам мониторинг и публичный URL.

## 8.4. Что такое Node-RED

**Node-RED** — это **визуальный редактор потоков** (flow-based programming), созданный IBM. Ты соединяешь «ноды» линиями, как в Unreal Blueprint или Scratch, и получаешь работающую программу без кода.

### Зачем Node-RED в нашем проекте

**Реальных датчиков у нас нет** (диплом, не серьёзный завод). Node-RED у нас **симулирует** датчики:

- 3 inject-кнопки: «Water Alert 2/5», «Fire Alert 2/3», «Reset 2/5 Water» — для ручного теста
- 1 inject + 1 function: heartbeat каждые 30 сек (54 датчика шлют NORMAL чтобы бэк знал что они живые)
- 1 MQTT-out: публикует всё в HiveMQ

В реальном проекте этот блок заменился бы физическими датчиками с прошивкой ESP32/Arduino, которые сами шлют MQTT.

### Наш Node-RED flow (вид сверху)

```
[inject "Heartbeat all 54" (every 30s)]──→[function "Build 54 heartbeats"]──┐
                                                                            │
                                                                            ▼
[inject "Water Alert 2/5"]────────────────────────────────────────→[MQTT-out "Publish Sensor Alert"]
[inject "Fire Alert 2/3"]─────────────────────────────────────────→
[inject "Reset 2/5 Water"]────────────────────────────────────────→
```

### Function-нода — как из одного клика получается 54 сообщения

```javascript
const msgs = [];
const types = [
  ['w', 'WATER'],
  ['s', 'SMOKE'],
];
for (let e = 1; e <= 3; e++) {
  for (let f = 1; f <= 9; f++) {
    for (const t of types) {
      msgs.push({
        topic: `smartresidency/sensors/${e}/${f}/${t[1]}`,
        payload: {
          id: `${t[0]}-${e}-${f}`,
          type: t[1],
          entrance_num: e,
          floor: f,
          status: 'NORMAL',
        },
      });
    }
  }
}
return [msgs];
```

`return [msgs]` — это синтаксис Node-RED: внешний массив = выходные порты (у нас 1), внутренний массив = N сообщений за раз.

## 8.5. Полный поток сообщения от датчика до телефона

Вот **главная диаграмма** — её должна знать назубок:

```
┌──────────┐     MQTT       ┌──────────────┐    MQTT      ┌─────────┐
│ Node-RED │ ──── publish ──▶│ HiveMQ Cloud │ ── deliver ─▶│ Go-бэк  │
│ (sensor) │   topic+payload │   (broker)   │              │(subscr.)│
└──────────┘                 └──────────────┘              └────┬────┘
                                                                │
                                                                │ 1. INSERT/UPDATE в БД
                                                                │ 2. Если NORMAL→ALERT:
                                                                │    создать sensor_event
                                                                │ 3. Вызвать NotifyEvent
                                                                ▼
                                                          ┌───────────┐
                                                          │ Firebase  │
                                                          │ Admin SDK │
                                                          └─────┬─────┘
                                                                │ HTTPS
                                                                ▼
                                                          ┌───────────┐
                                                          │ Google    │
                                                          │ FCM       │
                                                          └─────┬─────┘
                                                                │ push
                                                                ▼
                                                          ┌───────────┐
                                                          │ Flutter   │
                                                          │  на тлф.  │
                                                          └───────────┘
```

### Что происходит в Go-бэке при получении MQTT (`internal/mqtt/client.go` функция `handleSensor`)

```go
// 1. Распарсили JSON из payload
var msg sensorMsg
json.Unmarshal(m.Payload(), &msg)

// 2. Считали предыдущий статус из БД
var prev string
hadRow := s.db.QueryRow(ctx, `SELECT status FROM sensors WHERE id=$1`, msg.ID).Scan(&prev) == nil

// 3. UPSERT: вставляем или обновляем, last_seen_at = NOW() всегда
s.db.Exec(ctx, `INSERT INTO sensors ... ON CONFLICT DO UPDATE SET status=..., last_seen_at=NOW()`)

// 4. Если переход NORMAL→ALERT или OFFLINE→ALERT — создаём событие
if hadRow && msg.Status == "ALERT" && (prev == "NORMAL" || prev == "OFFLINE") {
    var eventID string
    s.db.QueryRow(ctx, `INSERT INTO sensor_events ... RETURNING id`).Scan(&eventID)

    // 5. Шлём push асинхронно (через горутину, чтобы не задерживать обработку MQTT)
    go s.notifier.NotifyEvent(ctx, eventID)
}

// 6. Если есть SSE-хаб — броадкастим админам
if s.bcast != nil && (!hadRow || prev != msg.Status) {
    s.bcast.Broadcast("sensor_update", ...)
}
```

---

# Часть 9. Firebase Cloud Messaging (FCM) — пуш-уведомления

## 9.1. Что такое FCM

**Firebase Cloud Messaging** — сервис Google для доставки пушей на Android, iOS, Web. Это **бесплатно** и **универсально** (один API на все платформы).

### Как FCM работает архитектурно

```
[Бэк]──HTTPS──▶[Google FCM]──push──▶[Устройство]
   ▲                                       │
   │  токен                                │
   └─────────────────register──────────────┘
```

1. Приложение на телефоне при запуске регистрируется в FCM (через `google-services.json` — это конфиг-файл Firebase для **клиентской** стороны: project_id, sender_id, API-key. Лежит в `android/app/` во Flutter-проекте. **Не путать** с `firebase-credentials.json` — это для бэка, с приватным ключом). Firebase возвращает **device token** (длинная строка).
2. Приложение шлёт этот токен на наш бэк (`POST /users/me/fcm-token`).
3. Бэк хранит токен в таблице `fcm_tokens`, привязанный к user_id.
4. Когда нужен пуш — бэк через Firebase Admin SDK шлёт сообщение с указанием token. Google роутит на устройство.

## 9.2. Что такое Firebase Admin SDK

**Firebase Admin SDK** — Go-библиотека от Google, через которую бэк обращается к FCM. Аутентификация через **service account** — JSON-файл с приватным ключом (`firebase-credentials.json` у нас).

### Создание объекта Sender (`internal/fcm/sender.go`):

```go
func New(ctx context.Context, credPath string, db *pgxpool.Pool) (*Sender, error) {
    app, _ := firebase.NewApp(ctx, nil, option.WithCredentialsFile(credPath))
    msg, _ := app.Messaging(ctx)
    return &Sender{db: db, messaging: msg}, nil
}
```

### Как шлём пуш (упрощённо):

```go
multicast := &messaging.MulticastMessage{
    Tokens: tokens,                    // массив FCM-токенов жителей подъезда
    Data:   data,                      // map[string]string с полями события
    Android: &messaging.AndroidConfig{
        Priority: "high",
    },
}
resp, _ := s.messaging.SendEachForMulticast(ctx, multicast)
```

### Почему **data-only**, а не notification

Если у сообщения есть поле `notification` — Android сам показывает баннер из системы, и приложение **не получает onMessage** в killed-состоянии. Если у нас **только data** — Flutter полностью контролирует отображение (через `flutter_local_notifications`). Это даёт более красивые баннеры с кнопками.

## 9.3. Адресная доставка (только жителям нужного подъезда)

В `NotifyEvent`:

```go
rows, _ := s.db.Query(ctx, `
    SELECT t.token FROM fcm_tokens t
    JOIN profiles p ON p.id = t.user_id
    WHERE p.entrance = $1                       -- только тот подъезд
      AND p.verification_status = 'approved'    -- только верифицированные
`, entrance)
```

Это **JOIN** двух таблиц по `user_id`. Если в подъезде живут 10 жителей и все верифицированы — пуш получат 10 устройств.

## 9.4. Что такое Service Account (Firebase credentials)

`firebase-credentials.json` — это **service account**. Чтобы понять что это, нужно знать про **OAuth 2.0**.

**OAuth 2.0** — стандарт авторизации в облаке. Идея: вместо того чтобы передавать сервер пароль, передаётся **токен** с ограниченными правами.

У Firebase два способа авторизоваться:

1. **Через юзера** (логин в Firebase Console) — для веб-интерфейса
2. **Через service account** — для серверов

Service account = «робот-юзер» который имеет:

- email (типа `firebase-adminsdk-abc123@smart-residency-e3404.iam.gserviceaccount.com`)
- приватный RSA-ключ (внутри JSON-файла)
- набор разрешений (у нас «может слать FCM и работать с Firestore»)

Когда Firebase Admin SDK делает запрос к Google FCM, он:

1. Создаёт JWT с claims (sender_id, scope, exp)
2. Подписывает приватным ключом из JSON (RSA-256 — асимметричное)
3. Шлёт в OAuth-эндпоинт Google, получает access token
4. С access token дёргает FCM API

Всё это происходит **под капотом** — мы просто пишем `s.messaging.SendEachForMulticast(...)`.

**Безопасность:** `firebase-credentials.json` — секрет. Если утечёт — кто угодно сможет слать пуши от нашего имени. Поэтому в .gitignore.

## 9.5. Notification message vs Data message в FCM (важная разница)

В FCM **два типа сообщений**:

**Notification message:**

```json
{
  "notification": {
    "title": "Тревога",
    "body": "Затопление 5 этаж"
  },
  "token": "abc..."
}
```

Что делает: Android **сам** показывает баннер в системе. Приложение НЕ получает callback в killed/background. Текст и заголовок фиксированы в момент отправки.

**Data message:**

```json
{
  "data": {
    "kind": "sensor_alert",
    "event_id": "evt-34",
    "title": "Тревога",
    "body": "...",
    "entrance_num": "2",
    "floor": "5"
  },
  "token": "abc..."
}
```

Что делает: Android передаёт это **в приложение**, всегда вызывает `_firebaseBackgroundHandler`. Приложение само решает что показывать.

**Мы используем только data-message.** Почему:

- Контроль над UI баннера (можно ставить кастомные иконки, действия, локализацию)
- Можно прикрепить `event_id` чтобы по тапу открыть нужную страницу
- Приложение в любом случае получает callback (нужно для обновления state)

## 9.6. JWT-токены в FCM (важный момент про время)

Firebase Admin SDK подписывает каждый запрос **своим JWT** (для аутентификации в Google). В этом JWT поля `iat` (issued at) и `exp` (expires) считаются по **системным часам**. Если часы Windows отстают/спешат больше чем на 5 минут — Google возвращает:

```
invalid_grant: Invalid JWT: Token must be a short-lived token (60 minutes) and in a reasonable timeframe
```

Лечится синхронизацией часов: Settings → Date & Time → Sync now.

---

# Часть 10. OFFLINE-детекция — heartbeat + sweeper

## 10.1. Зачем нужен OFFLINE-статус

Без него: если датчик сломался / провод перерезали / Node-RED упал — мы не знаем, что у нас «слепая зона». На admin-дашборде все 54 датчика остаются NORMAL, и это иллюзия безопасности.

С OFFLINE-статусом: если датчик молчит >60 сек → переводим в OFFLINE, шлём пуш админу.

## 10.2. Как работает heartbeat

В Node-RED каждые 30 сек шлём 54 publish со status=NORMAL. На бэке:

```go
// При каждом сообщении:
s.db.Exec(ctx, `INSERT ... ON CONFLICT (id) DO UPDATE SET ..., last_seen_at = NOW()`)
```

`last_seen_at` колонка хранит время последнего MQTT-сообщения от этого датчика. Это **сердцебиение системы**.

## 10.3. Sweeper-горутина (`internal/sensors/sweeper.go`)

Горутина = функция запущенная параллельно через `go func(){...}()`. У нас она запускается из main.go:

```go
go sensors.NewOfflineSweeper(pool, offlineNotifier, hub).Run(ctx)
```

И внутри:

```go
func (s *OfflineSweeper) Run(ctx context.Context) {
    t := time.NewTicker(s.Interval)  // каждые 15 сек
    for {
        select {
        case <-ctx.Done(): return     // программа завершается — выходим
        case <-t.C:                   // тик таймера
            s.sweepOnce(ctx)
        }
    }
}
```

`sweepOnce` делает SQL-запрос:

```sql
UPDATE sensors
SET status = 'OFFLINE', last_updated = NOW()
WHERE status != 'OFFLINE'
  AND last_seen_at < NOW() - make_interval(secs => $1)
RETURNING id, type, entrance_num, floor
```

Это **atomic**: одним запросом находим всех «замолчавших» и помечаем их OFFLINE. `RETURNING` отдаёт нам список изменённых строк — для каждой шлём FCM-пуш админу и SSE-broadcast.

Параметры по умолчанию: проверка каждые 15 сек, порог OFFLINE = 60 сек.

---

# Часть 11. SSE — реал-тайм для админ-страницы

## 11.1. Что такое SSE

**SSE (Server-Sent Events)** — стандарт HTML5 для одностороннего push'а от сервера к браузеру через стандартный HTTP. Клиент делает один long-lived GET-запрос, а сервер по нему пишет события в течение всего соединения.

### Формат фрейма (текстовый):

```
event: sensor_update
data: {"id":"w-2-5","status":"ALERT",...}

event: event_new
data: {"id":"evt-34",...}

```

(пустая строка = конец события)

### SSE vs WebSocket vs Polling

|                | SSE                  | WebSocket     | Polling                        |
| -------------- | -------------------- | ------------- | ------------------------------ |
| Направление    | Только сервер→клиент | Двусторонне   | Только запрос-ответ            |
| Протокол       | HTTP/HTTPS обычный   | Свой (ws://)  | HTTP                           |
| Сложность      | Простой              | Сложнее       | Очень простой                  |
| Авто-reconnect | Да (браузер сам)     | Нет (вручную) | N/A                            |
| Нагрузка       | Низкая               | Низкая        | Высокая (запросы каждые N сек) |

Мы выбрали SSE потому что:

1. Нам нужно только сервер→клиент (новые события датчиков и обновления)
2. Через стандартный HTTP (легко работает с прокси)
3. Автоматический реконнект в стандартных клиентах
4. Намного проще WebSocket в реализации

## 11.2. Наш SSE-hub (`internal/sse/hub.go`)

**Hub** — это «комната» для слушателей. У него:

- `clients map[chan []byte]struct{}` — кто подписан (каналы Go для каждого клиента)
- `Subscribe()` — создать канал, добавить в map
- `Unsubscribe()` — удалить из map и закрыть канал
- `Broadcast(event, data)` — на каждый канал отправить фрейм

```go
func (h *Hub) Broadcast(event string, data any) error {
    payload, _ := json.Marshal(data)
    frame := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload))
    h.mu.RLock()
    for ch := range h.clients {
        select {
        case ch <- frame:        // отправили
        default:                 // канал переполнен — скипаем (защита от медленных клиентов)
        }
    }
    h.mu.RUnlock()
    return nil
}
```

**Mutex** (мьютекс) — блокировка для безопасной работы с map из нескольких горутин. `RWMutex` позволяет много читателей одновременно, но writers эксклюзивны.

## 11.3. Stream-handler (внутри `handler/sensors.go`)

```go
func (h *SensorHandler) Stream(c *gin.Context) {
    // ...check admin role

    // Заголовки для SSE
    c.Writer.Header().Set("Content-Type", "text/event-stream")
    c.Writer.Header().Set("Cache-Control", "no-cache")

    ch := h.hub.Subscribe()
    defer h.hub.Unsubscribe(ch)

    flusher := c.Writer.(http.Flusher)
    io.WriteString(c.Writer, ":connected\n\n")
    flusher.Flush()  // сразу отправить байты клиенту, не буферизировать

    ping := time.NewTicker(15 * time.Second)
    for {
        select {
        case <-c.Request.Context().Done(): return   // клиент отключился
        case frame := <-ch:                          // что-то прилетело из hub
            c.Writer.Write(frame); flusher.Flush()
        case <-ping.C:                               // keepalive
            io.WriteString(c.Writer, ":ping\n\n"); flusher.Flush()
        }
    }
}
```

Когда новое событие происходит (например, NORMAL→ALERT датчика), `mqtt/client.go` вызывает `hub.Broadcast("event_new", ...)`. Hub разошлёт это всем подключённым админам, и тот цикл выше прочитает из канала и запишет в HTTP-ответ.

---

# Часть 12. Связь бэка с Flutter

## 12.1. Что такое Flutter

**Flutter** — UI-фреймворк от Google. Один код на Dart-языке компилируется в нативное приложение для Android и iOS. Аналоги: React Native, Native Android (Kotlin), Native iOS (Swift).

## 12.2. Как Flutter общается с нашим бэком

### REST API через HTTP/JSON

Flutter использует пакет `dio` или `http` для HTTP-запросов:

```dart
final response = await dio.get(
  'http://10.0.2.2:8080/api/v1/sensors/events/evt-34',
  options: Options(headers: {'Authorization': 'Bearer $token'}),
);
final data = response.data;  // распарсенный JSON
```

`10.0.2.2` — это особый IP внутри Android-эмулятора, через который виден localhost хоста.

### FCM-push для реал-тайм-уведомлений (без открытого приложения)

Когда приходит push:

1. Google FCM доставляет на устройство.
2. Flutter получает в `_firebaseBackgroundHandler` (в killed/background) или `FirebaseMessaging.onMessage` (когда приложение открыто).
3. Показывает локальный баннер.
4. По тапу на баннер открывает нужную страницу события (через `consumePendingOpenedMessage`).

### SSE-stream для реал-тайма в открытом админ-приложении

Когда админ открыл админ-страницу:

1. Flutter подключается к `GET /admin/sensors/stream?token=<JWT>` через пакет `flutter_client_sse`.
2. Получает поток событий.
3. На каждое `sensor_update` обновляет цвет ячейки в сетке.

## 12.3. Полный жизненный цикл «нажал кнопку → пришёл пуш»

1. **Mansur** жмёт в Node-RED inject «Water Alert 2/5»
2. **Node-RED** через MQTT publish → `smartresidency/sensors/2/5/WATER` с payload `{"id":"w-2-5","status":"ALERT",...}`
3. **HiveMQ Cloud** получает MQTT-фрейм, видит что наш Go-бэк подписан → шлёт ему копию
4. **Go-бэк** (`handleSensor`):
   - Считал prev=NORMAL из БД
   - UPSERT в sensors: status='ALERT', last_seen_at=NOW()
   - Переход NORMAL→ALERT → INSERT в sensor_events: id='evt-35', status='DETECTED'
   - Hub.Broadcast("sensor_update", ...) и Hub.Broadcast("event_new", ...) → админ-Flutter на SSE-stream получит фреймы
   - В горутине: NotifyEvent("evt-35")
5. **fcm.Sender.NotifyEvent**:
   - SELECT токены жителей подъезда 2 у которых approved
   - Firebase Admin SDK → шлёт multicast с data-message в Google FCM
6. **Google FCM** доставляет на телефоны жителей подъезда 2
7. **Flutter** (в killed-state) — `_firebaseBackgroundHandler` ловит data-message → показывает локальный баннер
8. **Житель** тапает баннер → приложение открывается → `consumePendingOpenedMessage` читает event_id из SharedPreferences → навигирует на страницу события → делает `GET /sensors/events/evt-35` → видит timeline и детали

**Этот сценарий — главный что нужно знать наизусть.**

## 12.4. Сценарий «Верификация жителя» (второй ключевой флоу)

Не только IoT — у нас есть классический flow с документами и одобрением. Комиссия может спросить «как у вас работает верификация резидента?». Вот полный путь:

1. **Регистрация.** Житель ставит приложение, регистрируется (`POST /auth/register`). В БД создаются строки в `users` (id, email, hash пароля) и `profiles` (role='resident', verification_status='not_submitted').

2. **Отправка заявки.** В приложении житель заполняет форму (адрес, подъезд, этаж, квартира) и **прикладывает фото документов** (паспорт / договор аренды). Flutter делает:
   - `POST /verification/requests` — JSON с requested_role + entrance/floor/apartment
   - `POST /verification/requests/:id/documents` — загрузка файлов через `multipart/form-data`

   Бэк сохраняет файлы в папку `uploads/verification-docs/<user_id>/` и пишет пути в таблицу `verification_documents`. `verification_requests.status = 'pending'`.

3. **Просмотр админом.** Админ открывает свою страницу «Заявки на верификацию», вызывает `GET /verification/requests` (без фильтра — админ видит все pending). Видит список с фотками документов (которые отдаются через `GET /uploads/...` — это статический файл-сервер).

4. **Одобрение/отказ.** Админ нажимает «Одобрить» → Flutter шлёт `PUT /verification/requests/:id/status` с `{"status":"approved"}`. Бэк:
   - `UPDATE verification_requests SET status='approved', reviewed_by=<admin_id>, reviewed_at=NOW() WHERE id=$1`
   - **Копирует** entrance/floor/apartment из verification_requests в profiles (через COALESCE — не перезаписывая если житель уже указал свои)
   - `UPDATE profiles SET verification_status='approved' WHERE id=<user_id>`

5. **Результат.** Теперь у жителя в `profiles` стоит entrance=2, floor=5, verification_status=approved. Это значит:
   - Он попадает в SELECT'ы для FCM-доставки (получает пуши о датчиках своего подъезда)
   - Может выписывать гостевые пропуски, открывать шлагбаум и т.д.

### Ключевая мысль для защиты

**Документы хранятся как файлы**, а не в БД. В БД только пути. Это правильно: бинарные файлы в БД раздувают её и замедляют запросы. Альтернатива в продакшене — S3 / MinIO / Cloud Storage.

---

# Часть 13. Безопасность

## 13.1. TLS на MQTT

Все MQTT-сообщения шифруются TLS (порт 8883 у HiveMQ). Кто-то посередине не может ни прочесть, ни подменить.

## 13.2. bcrypt для паролей

Хешируются с автоматической солью. Перебор по словарю нереален.

## 13.3. JWT-подпись

Без знания JWT_SECRET невозможно подделать токен с чужим user_id.

## 13.4. RBAC (Role-Based Access Control)

Каждый чувствительный эндпоинт проверяет `c.GetString("user_role") == "admin"`. Резидент не может вызвать `/admin/*`.

## 13.5. Резидент видит только свой подъезд

В `GET /sensors/events/:id`:

```go
if role != "admin" {
    h.db.QueryRow(ctx, `SELECT entrance FROM profiles WHERE id=$1`, userID).Scan(&ent)
    if ent == nil || *ent != e.EntranceNum {
        c.JSON(403, gin.H{"error": "not your entrance"}); return
    }
}
```

## 13.6. SQL injection защищена параметризацией

```go
h.db.Query(ctx, `SELECT ... WHERE id=$1`, userInput)   // ОК — $1 параметр
// НЕ ТАК: h.db.Query(ctx, "SELECT ... WHERE id='"+userInput+"'")   // SQL-инъекция
```

## 13.7. Удаление FCM-токена при logout

При выходе Flutter вызывает `POST /users/me/fcm-token/delete` чтобы перестать получать пуши на устройстве. Проверка `WHERE token=$1 AND user_id=$2` гарантирует что один юзер не удалит чужой токен.

## 13.8. HTTPS на бэке — у нас НЕТ (важный момент для защиты)

⚠️ **Этот вопрос комиссия задаёт почти всегда. Знай ответ.**

У нас Go-бэк слушает на **обычном HTTP** (порт 8080), не HTTPS. Все запросы Flutter↔Go идут **в открытом виде**.

**Где у нас шифрование ЕСТЬ:**

- MQTT через HiveMQ — **TLS:8883** (Node-RED ↔ Brokeр ↔ Go)
- Firebase Admin SDK ↔ Google FCM — **HTTPS** (внутри SDK, нам не виден)
- Google FCM ↔ Flutter — **HTTPS** (поверх Google-инфраструктуры)

**Что отвечать комиссии:**

> «В дипломном проекте мы запускаем бэк локально, поэтому используем HTTP. В продакшене мы бы поставили **reverse proxy** (например, Nginx или Caddy) перед нашим Go-приложением, и proxy бы терминировал TLS — добавлял HTTPS-обвязку. Сертификат можно получить бесплатно через Let's Encrypt. Сам Go-код менять не нужно — proxy отдаёт расшифрованный HTTP на localhost:8080.»

Это **правильный архитектурный паттерн** (TLS termination at proxy), а не костыль. Большинство продакшен-систем работают именно так.

**Что НЕ говорить:**

- ❌ «У нас нет шифрования» — есть, на MQTT и FCM-каналах
- ❌ «Это небезопасно» — для локального диплома сойдёт

## 13.9. Идемпотентность

**Идемпотентность** — свойство операции, при котором повторный её вызов с теми же параметрами даёт **тот же результат** и не ломает систему.

Зачем: если клиент шлёт запрос и не дождался ответа (плохая сеть) — он может повторить. Если эндпоинт идемпотентен, ничего страшного не случится.

Где идемпотентность у нас:

| Где                    | Как реализована                                                                                                      |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------- |
| Миграции БД            | `CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS` — можно прогонять много раз                                 |
| Регистрация FCM-токена | `INSERT ... ON CONFLICT (token) DO UPDATE` — UPSERT                                                                  |
| MQTT-сообщения         | `INSERT ... ON CONFLICT (id) DO UPDATE` для sensors — каждое сообщение приводит таблицу к одному и тому же состоянию |
| FCM-токен delete       | Если токена уже нет — всё равно вернёт 200 OK, никаких ошибок                                                        |
| Reset датчика          | Можно сбрасывать NORMAL→NORMAL много раз, ничего плохого не произойдёт                                               |

**НЕ идемпотентные** операции у нас: `POST /service-requests` (каждый вызов создаёт новую заявку), `PATCH /admin/sensors/events/:id/status` с разным `status` (меняет состояние).

---

# Часть 13b. Парковка и шлагбаум — новые модули

## 13b.1. Что добавили (краткий обзор)

В финальных коммитах мы (командой: Mansur + тиммейт) добавили полноценный паркинг-модуль:

| Модуль             | Что делает                                                    |
| ------------------ | ------------------------------------------------------------- |
| **vehicles**       | Жители регистрируют номера своих авто                         |
| **barrier v2**     | Сканирование номера → проверка по БД → MQTT-команда «открыть» |
| **admin_barrier**  | Ручное открытие шлагбаума администратором                     |
| **parking**        | Парковочные места, бронирования, история въезда/выезда        |
| **parking_permit** | Гостевые парковочные пропуска                                 |
| **notifications**  | In-app уведомления (хранятся в БД, читаются из приложения)    |

## 13b.2. Таблицы БД (миграции 004–010)

### vehicles — зарегистрированные автомобили

```sql
-- migration 004_barrier_vehicles.sql
CREATE TABLE vehicles (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plate_number VARCHAR(20) NOT NULL UNIQUE,
    brand        TEXT,
    model        TEXT,
    color        TEXT,
    is_active    BOOLEAN NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Житель добавляет свой номер через `POST /vehicles`. Один пользователь может иметь несколько машин.

### barrier_events — журнал шлагбаума

```sql
CREATE TABLE barrier_events (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    event_type   TEXT NOT NULL,   -- 'vehicle_scan' / 'manual_open' / 'guest_pass'
    direction    TEXT,            -- 'IN' / 'OUT'
    plate_number TEXT,
    vehicle_id   UUID REFERENCES vehicles(id),
    status       TEXT NOT NULL,   -- 'granted' / 'denied' / 'guest_granted'
    gate_id      TEXT DEFAULT 'main-gate',  -- для multi-gate (010)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### parking_spots, parking_bookings, parking_events (005)

```sql
-- parking_spots — физические места на парковке
CREATE TABLE parking_spots (
    id               UUID PRIMARY KEY,
    spot_number      VARCHAR(20) UNIQUE NOT NULL,
    type             VARCHAR(10) CHECK (type IN ('permanent','guest')),
    status           VARCHAR(10) DEFAULT 'free' CHECK (status IN ('free','occupied','reserved')),
    assigned_user_id UUID REFERENCES users(id)
);

-- parking_bookings — бронирование на время (гостевые)
CREATE TABLE parking_bookings (
    id         UUID PRIMARY KEY,
    spot_id    UUID REFERENCES parking_spots(id),
    user_id    UUID REFERENCES users(id),
    start_time TIMESTAMPTZ NOT NULL,
    end_time   TIMESTAMPTZ NOT NULL,
    status     VARCHAR(10) DEFAULT 'active'   -- 'active' / 'completed' / 'cancelled'
);

-- parking_events — история фактических занятий/освобождений
CREATE TABLE parking_events (
    id         UUID PRIMARY KEY,
    spot_id    UUID REFERENCES parking_spots(id),
    event_type VARCHAR(10) CHECK (event_type IN ('occupied','freed'))
);
```

### notifications — in-app уведомления (008)

```sql
CREATE TABLE notifications (
    id             UUID PRIMARY KEY,
    target_user_id UUID REFERENCES users(id),  -- null = всем
    target_role    VARCHAR(50),                -- 'admin' / 'resident' / null
    kind           VARCHAR(50) NOT NULL,       -- 'barrier_granted', 'parking_booked', ...
    title          TEXT NOT NULL,
    body           TEXT NOT NULL,
    data           JSONB,                      -- доп. данные для навигации в приложении
    read_at        TIMESTAMPTZ,                -- null = не прочитано
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Разница от FCM-пушей: **push может не дойти** (телефон был выключен), уведомление в БД — **всегда хранится** и прочитается когда пользователь откроет приложение.

## 13b.3. Как работает шлагбаум v2 (главная схема)

```
Телефон с камерой (Node-RED)
          │
          │ MQTT: smartresidency/barrier/scan
          │ {"plate":"A001BC","direction":"IN","gate_id":"main-gate"}
          ▼
      HiveMQ Cloud
          │
          ▼
      Go-бэк (barrier_v2.go)
          │
          ├─ Нашли в vehicles (is_active=true)?
          │    └─ ДА → INSERT barrier_events status='granted'
          │           → MQTT publish smartresidency/barrier/command {"action":"OPEN","direction":"IN"}
          │           → SendToUser (FCM-пуш владельцу авто)
          │
          ├─ Нашли активный parking_permit с этим номером?
          │    └─ ДА → INSERT status='guest_granted'
          │           → openBarrier()
          │           → SendToAdmins (уведомление)
          │
          └─ Нет совпадений?
               └─ INSERT status='denied'
                  → НЕ открываем шлагбаум
```

**MQTT-команда шлагбауму:**

```json
{ "action": "OPEN", "direction": "IN" }
```

Тема: `smartresidency/barrier/command`. Node-RED flow (`nodered_barrier_flow.json`) подписан на эту тему и имитирует физическое открытие.

Важно: это **обратный MQTT-поток** — Go-бэк теперь не только _подписчик_, но и _издатель_ для разных типов устройств.

## 13b.4. Парковочные пропуска (parking_permit)

Житель может выписать гостю парковочный пропуск:

- `POST /parking-permits` — создаём пропуск с номером авто гостя + диапазон дат
- Пропуск хранится в `parking_permits` (аналог `guest_access`, но для авто)
- При сканировании номера в `barrier_v2.go` мы сначала проверяем `vehicles`, потом — `parking_permits`

## 13b.5. Уведомления — разница FCM vs in-app

|                   | FCM push                             | In-app (notifications table)        |
| ----------------- | ------------------------------------ | ----------------------------------- |
| Доставка          | Немедленно, но «best-effort»         | Всегда в БД, достаётся при открытии |
| Телефон выключен  | Может потеряться (кеш FCM ограничен) | Сохраняется, прочитается позже      |
| Структура         | map[string]string (только строки)    | JSONB (любая вложенность)           |
| Чтение            | `onMessage` / background handler     | `GET /notifications`                |
| Отметка прочтения | Нет (системная)                      | `PATCH /notifications/:id/read`     |

Мы используем **оба** — FCM для мгновенного баннера, `notifications` как «ящик входящих».

---

# Часть 13c. Роли staff и guard

## 13c.1. Новые роли в системе (итоговый список)

| Роль       | Кто                           | Что может                                                  |
| ---------- | ----------------------------- | ---------------------------------------------------------- |
| `admin`    | Управляющая компания          | Всё                                                        |
| `resident` | Житель                        | Свои заявки, пропуска, парковка                            |
| `staff`    | Слесарь, электрик, уборщик... | Видит и берёт заявки своей специализации                   |
| `guard`    | Охранник на воротах           | Управление шлагбаумом/парковкой; создавать заявки НЕ может |

## 13c.2. Как хранится роль и специализация

**Миграция 012:**

```sql
ALTER TABLE profiles ADD COLUMN IF NOT EXISTS specialty VARCHAR(50);
ALTER TABLE service_requests
    ADD COLUMN IF NOT EXISTS assigned_to UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS taken_at    TIMESTAMPTZ;
```

- `profiles.role` = `'staff'` или `'guard'`
- `profiles.specialty` = `'plumbing'` | `'electrical'` | `'cleaning'` | `'garbage'` | `'intercom'` | `'elevator'`
- `service_requests.assigned_to` = UUID сотрудника которому назначена заявка (может быть NULL — «свободна»)
- `service_requests.taken_at` = когда сотрудник взял заявку в работу

## 13c.3. Specialty → category mapping (как фильтрация работает)

Каждая специализация охватывает определённые **категории заявок**:

```
plumbing    → [Протечка, Дверь/вход]
electrical  → [Электричество, Освещение]
cleaning    → [Уборка]
garbage     → [Вывоз мусора]
intercom    → [Домофон]
elevator    → [Лифт]
```

Когда `GET /service-requests`:

- **admin** видит **все** заявки
- **staff** видит только заявки категорий своей специализации (фильтр по `profiles.specialty` → map → `WHERE category = ANY($1)`)
- **resident** видит только свои заявки

```go
// В handler/service_requests.go
specialtyCategories := map[string][]string{
    "plumbing":   {"Протечка", "Дверь/вход"},
    "electrical": {"Электричество", "Освещение"},
    // ...
}
cats := specialtyCategories[specialty]  // lookup по specialty юзера
// SELECT ... WHERE category = ANY($1)  -- PostgreSQL ANY для массива
```

## 13c.4. Self-assign и admin-assign

**Self-assign (сотрудник сам берёт заявку):**

```
PATCH /service-requests/:id/take
```

Логика: если заявка `assigned_to IS NULL` и категория входит в specialty — разрешаем. Устанавливаем `assigned_to = me`, `taken_at = NOW()`, `status = in_progress`.

**Admin-assign (назначение конкретному сотруднику):**

```
POST /admin/service-requests/:id/assign
{"staff_id": "uuid..."}
```

**Что может и не может staff:**

- ✅ Видеть заявки своей специализации
- ✅ Брать незанятую заявку (`/take`)
- ✅ Менять статус только **своих** назначенных заявок (`in_progress` → `done`)
- ❌ Менять чужие заявки
- ❌ Создавать заявки (это право резидента)

**Guard:**

- ❌ Создавать заявки — при попытке бэк вернёт 403 «guard role cannot create requests»

## 13c.5. Эндпоинты для admin (staff-management)

```
GET  /admin/staff                   ← список всех staff с нагрузкой
GET  /admin/staff/:id/requests      ← заявки конкретного сотрудника
POST /admin/service-requests/:id/assign  ← назначить сотрудника на заявку
```

Ответ `GET /admin/staff` — для каждого сотрудника:

```json
{
  "id": "...",
  "full_name": "Иван Петров",
  "specialty": "plumbing",
  "in_progress_count": 3,
  "done_today_count": 5,
  "last_taken_at": "2026-05-15T10:30:00Z"
}
```

Всё считается одним SQL-запросом через `COUNT() FILTER (WHERE ...)` и `LEFT JOIN service_requests` — эффективно.

---

# Часть 14. Что отвечать на типовые вопросы комиссии

## «Почему не WebSocket?»

WebSocket — двухсторонний канал, у нас нужно только сервер→клиент. SSE проще, работает через стандартный HTTP, имеет встроенный реконнект.

## «Почему не Kafka/RabbitMQ а MQTT?»

Kafka — для бэкенд-микросервисов (обработка миллионов событий в секунду). MQTT — для IoT-устройств (низкое энергопотребление, маленькие фреймы, pub/sub). Для нашей задачи это правильный выбор.

## «Как масштабируете?»

- Go-бэк stateless (кроме SSE-hub) — горизонтально за load-balancer
- PostgreSQL — read-replicas или партиционирование `sensor_events` по времени
- HiveMQ Cloud сам масштабируется
- SSE-hub надо вынести в Redis Pub/Sub, если нужно несколько копий бэка

## «Почему PostgreSQL, а не NoSQL?»

У нас сильно связанные данные: user → profile → fcm_tokens; event → sensor. JOIN'ы делать удобно. NoSQL подошёл бы, если бы данные были без чёткой структуры.

## «Что если HiveMQ упадёт?»

- MQTT-клиент с `AutoReconnect=true` — переподключится автоматически
- retained-сообщения сохраняются в брокере (после восстановления текущее состояние датчиков сохранится)
- если совсем мёртв — рассмотреть мульти-брокер failover

## «Что если пуш не дошёл?»

- Firebase Admin SDK возвращает успех/неудачу для каждого токена
- Битые токены (юзер удалил приложение) — удаляем из БД (`messaging.IsUnregistered`)
- Гарантия доставки FCM «best-effort» — для критичных алертов можно дублировать через SMS

## «Что такое heartbeat?»

Каждые 30 сек все 54 датчика шлют MQTT-сообщение со status=NORMAL — это «я живой». Если бэк 60+ сек не слышит — помечает OFFLINE и шлёт пуш админу. Это стандартный паттерн в IoT для liveness detection.

## «Сколько одновременных подключений выдерживает ваш сервер?»

Go-бэк на одном инстансе спокойно держит ~10000 одновременных SSE/MQTT-подключений. Бутылочное горлышко скорее PostgreSQL.

## «Что если злоумышленник перехватит JWT-токен?»

Сценарий: токен украли (например через MITM на HTTP, или взломали телефон).

- Короткий срок жизни (24 часа) ограничивает ущерб
- В продакшене было бы 15 минут access + refresh-токен
- JWT нельзя «отозвать» в нашей реализации — единственный способ инвалидировать токен раньше срока — поменять `JWT_SECRET` (но тогда инвалидируются ВСЕ токены сразу)
- Решение для продакшена — blacklist отозванных токенов в Redis (но в нашем дипломном проекте этого нет)

## «Как обрабатываете ошибки на бэке?»

Go-стиль: ошибка возвращается из функции явно как второй параметр.

```go
result, err := someFunc()
if err != nil {
    log.Printf("error: %v", err)
    c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
    return
}
```

Никаких try/catch, panic мы не используем (только в фатальных ситуациях типа «не могу подключиться к БД»). Это **explicit error handling** — каждая ошибка обрабатывается там где появилась.

## «Есть ли тесты?»

Честно: **юнит-тестов у нас минимально** для дипломного проекта. Архитектура построена так что их легко добавить — все зависимости (БД, FCM, MQTT) обернуты в интерфейсы (EventNotifier, SensorPublisher, OfflineNotifier), поэтому можно подменять моками.

В продакшене обязательны: unit-тесты бизнес-логики, интеграционные тесты с testcontainers (поднимают настоящий Postgres в Docker), e2e-тесты через httptest.

Сейчас тестирование у нас **ручное** через PowerShell + Invoke-RestMethod + Node-RED-инжекты + Flutter-эмулятор.

## «Что такое идемпотентность и где она у вас?»

Идемпотентность = повторный вызов даёт тот же результат. У нас идемпотентны:

- Миграции (CREATE/ADD ... IF NOT EXISTS)
- UPSERT-операции (sensors, fcm_tokens)
- Delete FCM-токена (если нет — всё равно ОК)
- Reset датчика
- Heartbeat-сообщения

См. подробно раздел 13.9.

## «Как у вас сделана архитектура — монолит или микросервисы?»

**Монолит.** Один Go-сервис делает всё: auth, profiles, IoT, FCM, SSE. Для дипломного проекта это правильный выбор:

- Один проект, один деплой, одна БД
- Не нужно решать сложные проблемы межсервисной коммуникации, distributed transactions, service discovery
- Если в будущем понадобится — IoT-часть легко выделится в отдельный сервис (mqtt + sensors модули)

Микросервисы — отдельная архитектура, и её делают когда: команд много, фичи независимые, нужна разная скорость деплоя. У нас этого нет.

## «Как работает шлагбаум в вашей системе?»

Жители регистрируют номера своих авто через приложение (`POST /vehicles`). При въезде камера/Node-RED сканирует номер и шлёт его MQTT-сообщением на топик `smartresidency/barrier/scan`. Go-бэк проверяет: есть ли этот номер в таблице `vehicles` (или активный парковочный пропуск `parking_permits`). Если да — публикует MQTT-команду `{"action":"OPEN"}` на `smartresidency/barrier/command`, и Node-RED физически имитирует открытие. Если нет — статус `denied`, шлагбаум не открывается.

## «Чем отличаются роли staff и guard от обычного резидента?»

Staff — сотрудник УК (слесарь, электрик). Видит только заявки в УК своей специализации, может брать их в работу (`/take`) и менять статус. Guard — охранник: может управлять шлагбаумом и парковкой, но **не может создавать заявки** (логика: охранник не должен флудить заявками). Обе роли ограничены по сравнению с admin.

## «Как хранятся in-app уведомления и зачем они нужны?»

В таблице `notifications` с полями `target_user_id`, `kind`, `title`, `body`, `data (JSONB)`, `read_at`. FCM-пуши могут потеряться (телефон выключен, приложение убито агрессивным Android), а запись в БД — нет. Это «ящик входящих» — пользователь откроет приложение и увидит что пропустил. `GET /notifications` отдаёт непрочитанные, `PATCH /notifications/:id/read` помечает прочитанным.

## «Почему миграции применяются вручную через psql?»

Для диплома — простота. В продакшене обычно используют `golang-migrate/migrate` или `goose` — утилиты которые:

1. Хранят в служебной таблице `schema_migrations` номер последней применённой миграции
2. При старте бэка автоматически применяют все новые
3. Поддерживают rollback

Можно добавить за полчаса, мы не стали ради простоты.

---

# Часть 15. Как запустить проект с нуля

```powershell
# 1. Клонировать репозиторий, перейти в папку
cd C:\Users\user\Downloads\SmartRes

# 2. Установить зависимости Go
go mod download

# 3. Поднять PostgreSQL (у нас локальная установка PostgreSQL 18)

# 4. Применить миграции (порядок важен — каждая следующая зависит от предыдущих)
$psql = "C:\Program Files\PostgreSQL\18\bin\psql.exe"
$db   = "postgres://postgres:04062006@localhost:5432/smart_residency?sslmode=disable"
foreach ($m in 001..012) {
    $f = (Get-Item "migrations\0${m}_*.sql" 2>$null)[0]
    if ($f) { & $psql $db -f $f.FullName }
}

# 5. Заполнить .env (DATABASE_URL, JWT_SECRET, HIVEMQ_*, FIREBASE_CREDENTIALS_PATH)

# 6. Запустить Node-RED (порт 1880)

# 7. Запустить бэк
go run ./cmd/server

# Бэк слушает на :8080
# Flutter подключается к http://10.0.2.2:8080 (с эмулятора)
```

---

# Часть 16. Чек-лист «знаю наизусть» перед защитой

Прогони этот список вслух, проговаривая ответы:

- [ ] Что такое Go и почему выбрали именно его
- [ ] Что такое реляционная БД и какие у нас таблицы (минимум 6 назвать)
- [ ] Что такое UUID и зачем
- [ ] Что такое JWT, из чего состоит, как проверяется
- [ ] Что такое bcrypt и зачем
- [ ] Что такое MQTT, что такое publish/subscribe
- [ ] Что такое топик, QoS, retained-сообщение
- [ ] Что такое брокер, кто у нас брокер
- [ ] Что такое Node-RED, зачем он у нас
- [ ] Полная цепочка от inject в Node-RED до push на телефоне
- [ ] Что такое FCM, что такое токен устройства
- [ ] Почему data-only push, а не notification
- [ ] Что такое heartbeat и OFFLINE-детекция
- [ ] Что такое горутина в Go
- [ ] Что такое SSE и почему не WebSocket
- [ ] Как защищены пароли (bcrypt + соль)
- [ ] Как защищены MQTT (TLS:8883)
- [ ] Как защищён API (JWT + RBAC + entrance check)
- [ ] Что такое миграция БД
- [ ] **Почему у нас НЕТ HTTPS на бэке** и как бы решили в продакшене
- [ ] **Что такое идемпотентность** и где она у нас (минимум 3 примера)
- [ ] Сценарий верификации жителя (от регистрации до approved)
- [ ] Что такое сборщик мусора (GC) и почему Go быстрее Python
- [ ] Разница HTTP vs MQTT (минимум 3 пункта)
- [ ] Что такое UPSERT и зачем он нам
- [ ] Hashing vs encryption — две принципиально разные вещи
- [ ] У нас монолит, не микросервисы — почему
- [ ] Что такое шлагбаум v2 и как работает MQTT-команда открытия
- [ ] Как система определяет кому открыть шлагбаум (vehicles → parking_permits → denied)
- [ ] В чём разница FCM-пуш vs in-app уведомление в notifications таблице
- [ ] Какие роли есть в системе: admin / resident / staff / guard — что каждая может
- [ ] Что такое specialty у staff и как работает фильтрация заявок
- [ ] Что такое self-assign (`PATCH /service-requests/:id/take`) — кто и когда может
- [ ] Зачем нужны parking_spots / parking_bookings / parking_events — разница между ними
- [ ] Что такое parking_permit и чем отличается от guest_access (пропуск для гостя vs авто)

---

# Финальный совет

На защите тебя могут спросить любое подмножество этого. **Никогда не отвечай заученной фразой** — отвечай **своими словами**, даже если коряво. Комиссия видит когда ты понимаешь, а когда заучил.

Если не знаешь ответа на конкретный вопрос — лучше сказать «эту часть мы не делали, но если бы делали — подошёл бы такой-то инструмент по такой-то причине». Это лучше чем молчание или враньё.

Удачи на защите 16.05.2026.

— конспект подготовил Claude (бэк-Claude), обновлён 2026-05-15 (добавлены: парковка, шлагбаум v2, уведомления, роли staff/guard)

---

# Приложение A. Упрощённая ER-диаграмма БД (связи таблиц)

```
                  ┌──────────────────┐
                  │      users       │
                  │ id (UUID PK)     │
                  │ email UNIQUE     │
                  │ password_hash    │
                  └────────┬─────────┘
                           │ 1:1
              ┌────────────┼─────────────────────────┐
              │            │                         │
              ▼            ▼                         ▼
    ┌──────────────┐  ┌─────────────┐    ┌──────────────────┐
    │   profiles   │  │ fcm_tokens  │    │ verification_    │
    │ id (FK→users)│  │ user_id (FK)│    │ requests         │
    │ entrance     │  │ token PK    │    │ user_id (FK)     │
    │ floor        │  │ platform    │    │ status           │
    │ role         │  └─────────────┘    │ requested_role   │
    │ verif_status │                     └─────────┬────────┘
    └──────────────┘                               │ 1:N
              │                                    ▼
              │1:N                       ┌──────────────────┐
              ▼                          │ verification_    │
    ┌────────────────────┐               │ documents        │
    │ service_requests   │               │ file_path        │
    │ user_id (FK)       │               └──────────────────┘
    │ category, status   │
    └─────────┬──────────┘
              │ 1:N
              ▼
    ┌────────────────────┐
    │ request_photos     │
    │ request_id (FK)    │
    │ file_path          │
    └────────────────────┘


    ┌──────────────────┐ 1:N ┌──────────────────────┐
    │     vehicles     │     │   barrier_events     │
    │ id (UUID PK)     │◄────│ vehicle_id (FK, opt) │
    │ user_id (FK)     │     │ plate_number         │
    │ plate_number UNQ │     │ event_type, status   │
    │ is_active bool   │     │ direction, gate_id   │
    └──────────────────┘     └──────────────────────┘

    ┌──────────────────┐ 1:N ┌──────────────────────┐
    │  parking_spots   │     │  parking_bookings    │
    │ id (UUID PK)     │◄────│ spot_id (FK)         │
    │ spot_number UNQ  │     │ user_id (FK)         │
    │ type, status     │     │ start_time/end_time  │
    │ assigned_user_id │     │ status               │
    └──────────────────┘     └──────────────────────┘
              │ 1:N
              ▼
    ┌──────────────────┐
    │  parking_events  │
    │ spot_id (FK)     │
    │ event_type       │
    └──────────────────┘

    ┌──────────────────┐
    │  notifications   │
    │ id (UUID PK)     │
    │ target_user_id   │ ← null = широковещательное
    │ target_role      │
    │ kind, title, body│
    │ data (JSONB)     │
    │ read_at          │
    └──────────────────┘

    ┌──────────────────┐         ┌──────────────────────┐
    │     sensors      │ 1:N     │   sensor_events      │
    │ id (TEXT PK)     │◄────────│ sensor_id (FK)       │
    │ entrance_num     │         │ status               │
    │ floor, type      │         │ threat_type          │
    │ status           │         │ created_at           │
    │ last_seen_at     │         │ checking_at          │
    └──────────────────┘         │ confirmed_at         │
                                 │ confirmed_by (FK→u)  │
                                 └──────────────────────┘
```

- `1:1` — у одного user ровно один profile
- `1:N` — один user может иметь много fcm_tokens, много service_requests, много verification_requests
- `FK` — Foreign Key (внешний ключ)
- `PK` — Primary Key (первичный ключ)
- `→u` = ссылается на users(id)

---

# Приложение B. ASCII-схема MQTT pub/sub

```
                  smartresidency/sensors/2/5/WATER (topic)
                              │
              publish ┌───────┴────────┐ subscribe
                 ────►│                │◄────
            ┌────────▶│                │◄─────────────┐
            │         │  HiveMQ Cloud  │              │
            │         │    (broker)    │              │
            │         │   TLS:8883     │              │
   ┌────────┴────┐    │                │      ┌───────┴────────┐
   │  Node-RED   │    │                │      │  Go-бэк (mqtt  │
   │  (publisher │    └────────────────┘      │  subscriber)   │
   │  симулятор) │                            └────────────────┘
   └─────────────┘
                                              ┌────────────────┐
                                              │  Go-бэк (mqtt  │
                                              │  publisher для │
                                              │  reset)        │
                                              └────────────────┘
                                                      │
                                                      │ publish
                                                      ▼
                                             (тот же broker, тот же
                                              topic, retain=true)
```

Главное: **publisher и subscriber друг о друге не знают**. Они знают только про брокер. Это и есть «слабая связность» (loose coupling) — можно поменять сторону без переделок другой.

---

# Глоссарий — A-Z всех терминов одним списком

> Открывай этот раздел когда забыл что значит слово.

**ACID** — свойства транзакций БД: Atomicity, Consistency, Isolation, Durability.

**API** — Application Programming Interface, контракт общения между программами.

**Base64** — кодирование байтов в текстовые символы для передачи через текстовые каналы.

**bcrypt** — алгоритм хеширования паролей с автоматической солью, специально медленный.

**Broker (брокер)** — посредник в pub/sub-системе, принимает сообщения от издателей и раздаёт подписчикам. У нас — HiveMQ.

**CORS** — Cross-Origin Resource Sharing, механизм безопасности браузера для запросов между разными доменами.

**Context (context.Context)** — Go-объект с сигналом отмены и значениями, прокидываемый через цепочку вызовов.

**DNS** — Domain Name System, перевод доменных имён в IP-адреса.

**Encryption (шифрование)** — обратимое преобразование данных с ключом.

**EventSource** — JavaScript API для подключения к SSE.

**FCM** — Firebase Cloud Messaging, сервис Google для push-уведомлений.

**Firebase Admin SDK** — серверная библиотека для работы с Firebase (отправка пушей, работа с Firestore).

**Foreign Key (FK)** — внешний ключ, ссылка на строку другой таблицы.

**Gin** — HTTP-фреймворк для Go.

**Goroutine (горутина)** — лёгкий поток Go, запускается через `go func()`.

**Hashing (хеширование)** — необратимое преобразование данных в фиксированную строку.

**Heartbeat** — периодическое «я живой»-сообщение от датчика к серверу.

**HiveMQ** — облачный MQTT-брокер, используемый в проекте.

**HMAC** — Hash-based Message Authentication Code, проверка целостности через хеш + секрет.

**HTTP** — текстовый протокол запрос-ответ для веб-API.

**HTTPS** — HTTP поверх TLS (шифрованный).

**IoT** — Internet of Things, подключённые к сети устройства.

**JOIN** — SQL-операция склейки строк двух таблиц по условию.

**JSON** — формат структурированных текстовых данных.

**JWT** — JSON Web Token, токен с подписью, состоит из header.payload.signature.

**Long-lived connection** — TCP-соединение которое держится открытым долго (SSE, WebSocket, MQTT).

**Middleware** — промежуточный слой, выполняемый перед основным обработчиком запроса (auth, log, CORS).

**Migration (миграция)** — пошаговое изменение схемы БД, фиксируется в файле.

**MQTT** — Message Queuing Telemetry Transport, лёгкий pub/sub-протокол для IoT.

**Multicast** — отправка одного сообщения многим получателям (FCM SendEachForMulticast = разные tokens).

**Mutex** — взаимное исключение, блокировка для безопасного доступа к данным из разных горутин.

**Node-RED** — визуальный редактор потоков на Node.js, у нас используется для симуляции датчиков.

**OAuth 2.0** — стандарт авторизации через токены, используется в Firebase.

**OFFLINE-sweeper** — наша горутина, помечающая молчащие датчики как OFFLINE.

**Pool (connection pool)** — набор уже открытых соединений с БД для переиспользования.

**PostgreSQL** — реляционная БД, у нас в проекте.

**PRIMARY KEY (PK)** — первичный ключ, уникальный идентификатор строки.

**Publisher** — клиент в pub/sub, который шлёт сообщения брокеру.

**Pub/Sub** — паттерн «издатель/подписчик», основа MQTT.

**QoS (Quality of Service)** — уровни гарантии доставки в MQTT (0/1/2).

**RBAC** — Role-Based Access Control, контроль доступа на основе ролей (admin/resident).

**REST API** — стиль HTTP-API на основе ресурсов + методов GET/POST/PUT/PATCH/DELETE.

**Retained message** — MQTT-сообщение которое брокер хранит и отдаёт новым подписчикам.

**Service Account** — «робот-юзер» в OAuth, у нас — для Firebase Admin SDK.

**SQL** — Structured Query Language, язык запросов к реляционным БД.

**SSE** — Server-Sent Events, одностороннее потоковое HTTP-соединение сервер→клиент.

**Stateless** — без состояния, каждый запрос самодостаточен (REST-принцип).

**Struct** — структура данных в Go, аналог класса без методов.

**Subscriber** — клиент в pub/sub, который слушает сообщения с брокера.

**Sweeper** — наша горутина, периодически прочёсывающая БД для OFFLINE-датчиков.

**TCP/IP** — базовый стек интернета, протоколы транспортного и сетевого слоя.

**TLS** — Transport Layer Security, шифрование TCP-соединения. У нас на MQTT:8883.

**Topic** — иерархический путь сообщения в MQTT (например `smartresidency/sensors/2/5/WATER`).

**Transaction (транзакция)** — атомарная группа SQL-запросов.

**UPSERT** — INSERT … ON CONFLICT DO UPDATE, вставка с обновлением при конфликте ключа.

**UUID** — Universally Unique Identifier, 128-битный уникальный ID.

**Wildcard в MQTT** — `+` (один уровень) и `#` (много уровней) для подписки на группы топиков.

**Bcrypt** — см. bcrypt выше.

**CPU-bound** — программа, ограниченная скоростью процессора (расчёты, хеширование).

**Event-loop** — однопоточная очередь задач (модель JS/Node.js), асинхронный диспетчер.

**Garbage Collector (GC)** — автоматическая очистка памяти в Go/Java/Python.

**GIL** — Global Interpreter Lock в Python, мешает настоящей параллельности.

**google-services.json** — конфиг Firebase для клиента (Flutter), кладётся в `android/app/`.

**firebase-credentials.json** — секрет Firebase для сервера (Go-бэк), приватный RSA-ключ.

**Handshake (рукопожатие)** — обмен начальными пакетами при установке TCP/TLS-соединения.

**HTTPS** — HTTP+TLS. У нас на бэке НЕТ, есть только на FCM-канале и MQTT.

**I/O-bound** — программа, ограниченная скоростью диска/сети (типичный веб-API).

**Idempotency (идемпотентность)** — повторный вызов операции даёт тот же результат.

**Let's Encrypt** — бесплатный CA для получения TLS-сертификатов.

**MITM (Man-In-The-Middle)** — атака «человек посередине» — кто-то слушает или подменяет трафик.

**Monolith (монолит)** — одна программа со всеми фичами в одном процессе. У нас именно так.

**Nginx / Caddy** — reverse proxy для терминации TLS и роутинга.

**Reverse proxy** — посредник перед бэком, делает HTTPS, балансировку, кеш. У нас в проде был бы Nginx.

**Redis** — in-memory key-value хранилище. Часто используется для кеша, очередей, Pub/Sub.

**Rainbow table** — заранее посчитанная таблица «хеш → пароль» для взлома без соли. Защита — соль.

**SHA-256** — криптографическая хеш-функция, выход 256 бит. Используется в HS256-подписи JWT.

**Specialty (специализация)** — поле `profiles.specialty` для staff-роли. Определяет какие категории заявок видит и берёт сотрудник.

**Staff** — сотрудник УК (слесарь, электрик и др.). Роль в системе, видит только заявки своей специализации.

**Guard** — охранник. Роль в системе, управляет шлагбаумом и парковкой, но не может создавать заявки в УК.

**Self-assign** — механизм когда сотрудник сам берёт незанятую заявку через `PATCH /service-requests/:id/take`.

**Barrier v2** — модуль шлагбаума с MQTT-командами. Go-бэк публикует `{"action":"OPEN"}` на тему `smartresidency/barrier/command` после проверки номера авто.

**Parking spot** — физическое парковочное место. Тип: permanent (постоянное, для резидента) или guest (гостевое, для бронирования).

**Parking permit** — парковочный пропуск для гостевого авто. Выписывает резидент — номер авто гостя + диапазон дат.

**In-app notification** — уведомление сохранённое в таблице `notifications`, читается из приложения через `GET /notifications`. В отличие от FCM-пуша — не теряется если телефон был выключен.

**assigned_to** — поле `service_requests.assigned_to`, UUID сотрудника которому назначена заявка. NULL = заявка свободна.

**taken_at** — поле `service_requests.taken_at`, время когда сотрудник взял заявку через self-assign.

---

# История изменений документа

- **2026-05-11 v1** — первая версия (части 1-16)
- **2026-05-11 v2** — добавлены: Часть 0 (фундаментальные термины), раздел 2.4 (основы Go), 3.5-3.9 (SQL-команды, ключи, индексы, UUID, транзакции, pool), 6.3-6.5 (hashing vs encryption, HMAC, refresh token), 9.4-9.5 (Service Account, Notification vs Data), Приложения A-B (ER-диаграмма, MQTT-схема), Глоссарий
- **2026-05-11 v3** — добавлены: 0.11 (.env), 0.12 (TCP-handshake), расширен 2.1-2.2 (GC, event-loop, GIL, CPU/IO-bound, монолит≠микросервисы), 9.1 (разница google-services.json vs firebase-credentials.json), 12.4 (полный сценарий верификации жителя), 13.8 (почему у нас нет HTTPS — главный вопрос комиссии), 13.9 (идемпотентность), 5 новых вопросов в Часть 14 (JWT-перехват, error handling, тесты, монолит, миграции), 7 пунктов в чек-лист, ~15 терминов в глоссарий