package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"smartresidency/internal/db"
	"smartresidency/internal/fcm"
	"smartresidency/internal/handler"
	"smartresidency/internal/middleware"
	"smartresidency/internal/mqtt"
	"smartresidency/internal/parking"
	"smartresidency/internal/sensors"
	"smartresidency/internal/sse"
)

func main() {
	_ = godotenv.Load()

	pool, err := db.Connect(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal("db:", err)
	}
	defer pool.Close()

	ctx := context.Background()

	if err := db.RunMigrations(ctx, pool, "migrations"); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	if err := sensors.Seed(ctx, pool); err != nil {
		log.Printf("sensors seed: %v", err)
	} else {
		log.Println("sensors seeded")
	}

	if err := parking.Seed(ctx, pool); err != nil {
		log.Printf("parking seed: %v", err)
	} else {
		log.Println("parking seeded")
	}

	var notifier handler.EventNotifier
	var mqttNotifier mqtt.Notifier
	var offlineNotifier sensors.OfflineNotifier
	var barrierNotifier handler.BarrierNotifier
	var parkingNotifier mqtt.ParkingNotifier
	if credPath := os.Getenv("FIREBASE_CREDENTIALS_PATH"); credPath != "" {
		s, err := fcm.New(ctx, credPath, pool)
		if err != nil {
			log.Printf("fcm init: %v (push disabled)", err)
		} else {
			notifier = s
			mqttNotifier = s
			offlineNotifier = s
			barrierNotifier = s
			parkingNotifier = s
			log.Println("fcm initialized")
		}
	} else {
		log.Println("FIREBASE_CREDENTIALS_PATH not set — push disabled")
	}

	hub := sse.NewHub()
	go sensors.NewOfflineSweeper(pool, offlineNotifier, hub).Run(ctx)

	var publisher handler.SensorPublisher
	var sub *mqtt.Subscriber
	if url := os.Getenv("HIVEMQ_URL"); url != "" {
		cfg := mqtt.Config{
			URL:      url,
			Username: os.Getenv("HIVEMQ_USERNAME"),
			Password: os.Getenv("HIVEMQ_PASSWORD"),
			ClientID: os.Getenv("HIVEMQ_CLIENT_ID"),
		}
		s, err := mqtt.New(cfg, pool, mqttNotifier, hub)
		if err != nil {
			log.Printf("mqtt init: %v (subscriber disabled)", err)
		} else {
			sub = s
			publisher = sub
			defer sub.Close()
		}
	} else {
		log.Println("HIVEMQ_URL not set — mqtt disabled")
	}

	var publishFn func(topic, payload string) error
	if sub != nil {
		publishFn = sub.Publish
	}

	barrierV2H := handler.NewBarrierV2Handler(pool, barrierNotifier, publishFn)
	permitH := handler.NewParkingPermitHandler(pool, publishFn, barrierNotifier)
	if sub != nil {
		sub.SetBarrierCallback(barrierV2H.ProcessScanPlate)
		sub.SetParkingGateCallback(permitH.ProcessParkingGate)
		if parkingNotifier != nil {
			sub.SetParkingNotifier(parkingNotifier)
		}
	}
	vehicleH := handler.NewVehicleHandler(pool)
	adminBarrierH := handler.NewAdminBarrierHandler(pool, barrierV2H)

	r := gin.Default()

	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	r.Static("/uploads", "./uploads")

	api := r.Group("/api/v1")
	secret := os.Getenv("JWT_SECRET")

	authH := handler.NewAuthHandler(pool)
	api.POST("/auth/register", authH.Register)
	api.POST("/auth/login", authH.Login)

	priv := api.Group("/")
	priv.Use(middleware.Auth(secret))
	{
		priv.GET("/auth/me", authH.Me)
		priv.POST("/auth/refresh", authH.Refresh)

		profH := handler.NewProfileHandler(pool)
		priv.GET("/profiles/:id", profH.Get)
		priv.PUT("/profiles/:id", profH.Update)

		srH := handler.NewServiceRequestHandler(pool)
		priv.GET("/service-requests", srH.List)
		priv.POST("/service-requests", srH.Create)
		priv.PUT("/service-requests/:id", srH.UpdateStatus)
		priv.PATCH("/service-requests/:id/take", srH.Take)
		priv.POST("/service-requests/:id/photos", srH.UploadPhoto)
		priv.POST("/admin/service-requests/:id/assign", srH.Assign)
		priv.POST("/admin/service-requests/:id/resolve-appeal", srH.ResolveAppeal)

		staffH := handler.NewStaffHandler(pool)
		priv.GET("/admin/staff", staffH.List)
		priv.GET("/admin/staff/:id/requests", staffH.Requests)

		guestH := handler.NewGuestHandler(pool)
		priv.GET("/guest-access", guestH.List)
		priv.POST("/guest-access", guestH.Create)
		priv.PUT("/guest-access/:id/cancel", guestH.Cancel)

		barrierH := handler.NewBarrierHandler(pool)
		priv.GET("/barrier-logs", barrierH.List)
		priv.POST("/barrier-logs/open", barrierH.OpenBarrier)

		verH := handler.NewVerificationHandler(pool)
		priv.POST("/verification/requests", verH.Submit)
		priv.POST("/verification/requests/:id/documents", verH.UploadDocuments)
		priv.GET("/verification/requests", verH.List)
		priv.PUT("/verification/requests/:id/status", verH.UpdateStatus)

		sensorH := handler.NewSensorHandler(pool, notifier, publisher, hub)
		priv.GET("/sensors", sensorH.ListByEntrance)
		priv.GET("/sensors/events", sensorH.ListEvents)
		priv.GET("/sensors/events/:id", sensorH.GetEventDetail)
		priv.GET("/admin/sensors", sensorH.ListAll)
		priv.GET("/admin/sensors/stats", sensorH.Stats)
		priv.GET("/admin/sensors/stream", sensorH.Stream)
		priv.POST("/admin/sensors/:id/reset", sensorH.Reset)
		priv.PATCH("/admin/sensors/events/:id/status", sensorH.UpdateEventStatus)
		priv.POST("/admin/sensors/events/:id/notify", sensorH.NotifyEvent)

		fcmH := handler.NewFCMTokenHandler(pool)
		priv.POST("/users/me/fcm-token", fcmH.Register)
		priv.POST("/users/me/fcm-token/delete", fcmH.Delete)

		priv.GET("/vehicles", vehicleH.List)
		priv.POST("/vehicles", vehicleH.Create)
		priv.DELETE("/vehicles/:id", vehicleH.Delete)

		priv.POST("/barrier/scan-plate", barrierV2H.ScanPlate)
		priv.POST("/barrier/scan-qr", barrierV2H.ScanQR)
		priv.POST("/barrier/open-manual", barrierV2H.OpenManual)
		priv.GET("/barrier/events", barrierV2H.ListEvents)

		priv.GET("/admin/barrier/events", adminBarrierH.ListAll)
		priv.GET("/admin/barrier/unknown", adminBarrierH.ListUnknown)
		priv.POST("/admin/barrier/unknown/:id/approve", adminBarrierH.ApproveUnknown)
		priv.POST("/admin/barrier/unknown/:id/reject", adminBarrierH.RejectUnknown)

		parkingH := handler.NewParkingHandler(pool)
		priv.GET("/parking/spots", parkingH.ListSpots)
		priv.GET("/parking/bookings/my", parkingH.MyBookings)
		priv.POST("/parking/bookings", parkingH.CreateBooking)
		priv.PUT("/parking/bookings/:id/cancel", parkingH.CancelBooking)
		priv.GET("/admin/parking/spots", parkingH.AdminListSpots)
		priv.POST("/admin/parking/spots/:id/assign", parkingH.AdminAssignSpot)
		priv.GET("/admin/parking/bookings", parkingH.AdminListBookings)
		priv.GET("/admin/parking/events", parkingH.AdminListEvents)

		priv.GET("/parking/permit", permitH.MyPermits)
		priv.POST("/parking/permit", permitH.Submit)
		priv.POST("/parking/permit/:id/document", permitH.UploadDocument)
		priv.POST("/parking/gate/scan-plate", permitH.ScanParkingGate)
		priv.GET("/admin/parking/permits", permitH.AdminList)
		priv.PUT("/admin/parking/permits/:id/status", permitH.AdminReview)

		notifH := handler.NewNotificationsHandler(pool)
		priv.GET("/notifications", notifH.List)
		priv.GET("/notifications/unread-count", notifH.UnreadCount)
		priv.PUT("/notifications/:id/read", notifH.MarkRead)
		priv.PUT("/notifications/read-all", notifH.MarkAllRead)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("listening on :%s", port)
	log.Fatal(r.Run(":" + port))
}
