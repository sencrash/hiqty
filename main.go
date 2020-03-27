package main

import (
	"context"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"github.com/gomodule/redigo/redis"
	"github.com/joho/godotenv"
	"github.com/uppfinnarn/hiqty/media"
	"github.com/uppfinnarn/hiqty/media/soundcloud"
	"gopkg.in/urfave/cli.v2"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func populateServices(cc *cli.Context) error {
	// SoundCloud
	{
		clientID := cc.String("soundcloud-client-id")
		if clientID != "" {
			media.Register(soundcloud.New(
				cc.String("soundcloud-client-id"),
			))
			log.Info("Service Registered: soundcloud")
		} else {
			log.Warn("Service Unavailable: soundcloud")
		}
	}

	return nil
}

func actionRun(cc *cli.Context) error {
	token := cc.String("token")
	if token == "" {
		return cli.Exit("Missing bot token", 1)
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	redisAddr := cc.String("redis")
	pool := &redis.Pool{
		IdleTimeout: 2 * time.Minute,
		Dial: func() (redis.Conn, error) {
			return redis.Dial("tcp", redisAddr)
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}

	// Log connection state changes.
	session.AddHandler(func(_ *discordgo.Session, e *discordgo.Connect) {
		log.Info("Connected!")
	})
	session.AddHandler(func(_ *discordgo.Session, e *discordgo.Disconnect) {
		log.Warn("Disconnected!")
	})
	session.AddHandler(func(_ *discordgo.Session, e *discordgo.Resumed) {
		log.Info("Resumed!")
	})
	session.AddHandler(func(_ *discordgo.Session, e *discordgo.Ready) {
		log.WithFields(log.Fields{
			"protocol": e.Version,
			"username": fmt.Sprintf("%s#%s", e.User.Username, e.User.Discriminator),
		}).Info("Ready!")
	})

	// Run the Responder and the Player in goroutines.
	wg := sync.WaitGroup{}
	ctx, cancel := context.WithCancel(context.Background())

	responder := Responder{
		Session: session,
		Pool:    pool,
	}
	wg.Add(1)
	go func() {
		log.Info("Responder: Initializing")
		responder.Run(ctx)
		log.Info("Responder: Terminated")
		wg.Done()
	}()

	playerController := PlayerController{
		Session: session,
		Pool:    pool,
	}
	wg.Add(1)
	go func() {
		log.Info("PlayerController: Initializing")
		playerController.Run(ctx)
		log.Info("PlayerController: Terminated")
		wg.Done()
	}()

	// Connect to Discord.
	if err := session.Open(); err != nil {
		log.WithError(err).Error("Couldn't connect to Discord!")
		return err
	}

	// Wait for a signal before exiting.
	quit := make(chan os.Signal)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	sig := <-quit
	log.WithField("sig", sig).Info("Signal")
	signal.Reset()

	// Shut down subsystems, wait for them to finish.
	cancel()
	wg.Wait()

	return nil
}

func actionInfo(cc *cli.Context) error {
	token := cc.String("token")
	if token == "" {
		return cli.Exit("Missing bot token", 1)
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	u, err := session.User("@me")
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	app, err := session.Application("@me")
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	fmt.Printf("Authorized as: %s#%s (bot: %v)\n", u.Username, u.Discriminator, u.Bot)
	fmt.Printf("Application: %s (%s)\n", app.Name, app.ID)
	fmt.Printf("\n")

	fmt.Printf("Invite link:\n")
	fmt.Printf("https://discordapp.com/oauth2/authorize?client_id=%s&scope=bot&permissions=%d\n", app.ID, RequiredPermissions)
	return nil
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.WithError(err).Error("Couldn't load .env")
	}

	app := cli.App{}
	app.Name = "hiqty"
	app.Usage = "A high quality Discord music bot"
	app.HideVersion = true
	app.Flags = []cli.Flag{
		&cli.BoolFlag{
			Name:    "verbose",
			Aliases: []string{"v"},
			EnvVars: []string{"HIQTY_VERBOSE"},
			Usage:   "Log debug messages",
		},
		&cli.StringFlag{
			Name:    "redis",
			Aliases: []string{"r"},
			Usage:   "Redis address",
			EnvVars: []string{"HIQTY_REDIS"},
			Value:   "127.0.0.1:6379",
		},
		&cli.StringFlag{
			Name:    "soundcloud-client-id",
			Usage:   "Soundcloud Client ID",
			EnvVars: []string{"SOUNDCLOUD_CLIENT_ID"},
		},
	}
	app.Commands = []*cli.Command{
		&cli.Command{
			Name:   "run",
			Usage:  "Runs the bot interface + player",
			Action: actionRun,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "token",
					Aliases: []string{"t"},
					Usage:   "Discord token",
					EnvVars: []string{"HIQTY_BOT_TOKEN"},
				},
			},
		},
		&cli.Command{
			Name:   "info",
			Usage:  "Prints bot information and invite link",
			Action: actionInfo,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "token",
					Aliases: []string{"t"},
					Usage:   "Discord token",
					EnvVars: []string{"HIQTY_BOT_TOKEN"},
				},
			},
		},
	}
	app.Before = func(cc *cli.Context) error {
		if cc.Bool("verbose") {
			log.SetLevel(log.DebugLevel)
		}

		if err := populateServices(cc); err != nil {
			return err
		}

		return nil
	}
	if app.Run(os.Args) != nil {
		os.Exit(1)
	}
}
