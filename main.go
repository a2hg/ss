package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gin-gonic/gin"
	ginlimiter "github.com/julianshen/gin-limiter"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

type Config struct {
	Key      string   `json:"databaseKey"`
	Token    string   `json:"discordbotToken"`
	Searches Searches `json:"searches"`
	ID       string   `json:"channelID"`
}

type Searches struct {
	Name string `json:"databaseName"`
	Coll string `json:"databaseCollection"`
}

var (
	client *mongo.Client
	ctx    context.Context
	err    error
	s      *discordgo.Session
	config Config
)

func init() {
	fmt.Print(`
   __  _____________  __               
  /  |/  / ___/ __/ |/ /
 / /|_/ / /___\ \/    /
/_/  /_/\___/___/_/|_/       

`)

	config.LoadState()

	ctx, _ = context.WithTimeout(context.Background(), 9999999*time.Second)
	client, _ = mongo.Connect(ctx, options.Client().ApplyURI(config.Key))
}

func main() {
	app := &cli.App{
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: `Start the web API.`,
				Action: func(c *cli.Context) error {

					s, err = discordgo.New("Bot " + config.Token)
					if err != nil {
						log.Fatalf("Invalid bot parameters: %v", err)
					}

					err = s.Open()
					if err != nil {
						log.Fatalf("Cannot open the session: %v", err)
					}

					defer s.Close()

					start()
					return nil
				},
			},

			{
				Name:  "index",
				Usage: `Indexs your configs collection, this is required for first run usage.`,
				Action: func(c *cli.Context) error {
					keys := bsonx.Doc{{Key: "expirationTime", Value: bsonx.Int32(int32(1))}}
					idx := mongo.IndexModel{Keys: keys, Options: &options.IndexOptions{ExpireAfterSeconds: &[]int32{0}[0]}}
					_, err := client.Database(config.Searches.Name).Collection(config.Searches.Coll).Indexes().CreateOne(context.Background(), idx)
					if err != nil {
						log.Println("Error occurred while creating index", err)
					} else {
						log.Println("Index creation success")
					}
					return nil
				},
			},
		},

		HideHelp: false,
		Name:     "Scraper",
		Usage:    "This program grabs namemc info through embeds.",
		Version:  "1.00",
	}

	app.Run(os.Args)
}

func start() {
	r := gin.New()

	search := ginlimiter.NewRateLimiter(time.Minute, 10, func(ctx *gin.Context) (string, error) {
		return ctx.ClientIP(), nil
	})

	names := r.Group("/api")
	{
		names.GET("/search/:name", search.Middleware(), func(c *gin.Context) {
			search, droptime, found := checksearches(client.Database(config.Searches.Name).Collection(config.Searches.Coll), strings.ToLower(c.Param("name")))
			if !found {
				meow, _ := s.ChannelMessageSend(config.ID, "https://namemc.com/name/"+strings.ToLower(c.Param("name")))

				for len(meow.Embeds) == 0 {
					meow, _ = s.ChannelMessageSend(config.ID, "https://namemc.com/name/"+strings.ToLower(c.Param("name")))
				}

				if strings.Contains(meow.Embeds[0].Description, "Time of Availability") {
					droptimes, _ := time.Parse(time.RFC3339, strings.Split(strings.Split(meow.Embeds[0].Description, "Time of Availability: ")[1], ",")[0])
					search = strings.Split(strings.Split(meow.Embeds[0].Description, "Searches: ")[1], " / month")[0]
					go func() {
						client.Database(config.Searches.Name).Collection(config.Searches.Coll).InsertOne(ctx, bson.D{
							{Key: "expirationTime", Value: droptimes},
							{Key: "searches", Value: search},
							{Key: "name", Value: strings.ToLower(c.Param("name"))},
							{Key: "droptime", Value: droptimes.Unix()},
						})
					}()

					droptime = droptimes.Unix()
				} else {
					search = strings.Split(strings.Split(meow.Embeds[0].Description, "Searches: ")[1], " / month")[0]
					go func() {
						client.Database(config.Searches.Name).Collection(config.Searches.Coll).InsertOne(ctx, bson.D{
							{Key: "expirationTime", Value: time.Now().Add(1800 * time.Second)},
							{Key: "searches", Value: search},
							{Key: "name", Value: strings.ToLower(c.Param("name"))},
						})
					}()
				}
			}

			if droptime == 0 {
				c.JSON(http.StatusOK, gin.H{
					"searches": search,
					"name":     c.Param("name"),
				})
			} else {
				c.JSON(http.StatusOK, gin.H{
					"searches": search,
					"name":     c.Param("name"),
					"droptime": droptime,
				})
			}
		})
	}

	r.Run(":80")
}

func checksearches(collection *mongo.Collection, name string) (string, int64, bool) {
	var payload map[string]interface{}
	var found bool = false
	var searches string
	var droptime int64

	cursor, _ := collection.Find(context.Background(), bson.D{{"name", name}})

	for cursor.Next(context.TODO()) {
		elem := &bson.D{}
		if err := cursor.Decode(elem); err != nil {
			log.Println(err)
		}

		elems := elem.Map()

		maps, _ := json.Marshal(elems)

		json.Unmarshal(maps, &payload)

		found = true
		searches = payload["searches"].(string)
		if payload["droptime"] != nil {
			droptime = int64(payload["droptime"].(float64))
		}

	}

	return searches, droptime, found
}

func (s *Config) LoadState() {
	data, err := ReadFile("config.json")
	if err != nil {
		log.Println("No config file found, loading one.")
		s.LoadFromFile()

		s.Key = "MONGODB KEY"
		s.Token = "DISCORD BOT TOKEN"
		s.ID = "DISCORD CHANNEL ID"

		s.SaveConfig()
		os.Exit(0)
	}

	json.Unmarshal([]byte(data), s)
	s.LoadFromFile()
}

func (c *Config) LoadFromFile() {
	// Load a config file

	jsonFile, err := os.Open("config.json")
	// if we os.Open returns an error then handle it
	if err != nil {
		jsonFile, _ = os.Create("config.json")
	}
	byteValue, _ := ioutil.ReadAll(jsonFile)
	json.Unmarshal(byteValue, &c)
}

func (config *Config) SaveConfig() {
	WriteFile("config.json", string(config.ToJson()))
}

func (s *Config) ToJson() []byte {
	b, _ := json.MarshalIndent(s, "", "  ")
	return b
}

func WriteFile(path string, content string) {
	ioutil.WriteFile(path, []byte(content), 0644)
}

func ReadFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}
