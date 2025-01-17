package dllist

import (
	"NintendoChannel/constants"
	"NintendoChannel/gametdb"
	"NintendoChannel/info"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"runtime"
	"sync"

	colorFmt "github.com/fatih/color"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/wii-tools/lzx/lz10"
)

type List struct {
	Header          Header
	RatingsTable    []RatingTable
	TitleTypesTable []TitleTypeTable
	CompaniesTable  []CompanyTable
	TitleTable      []TitleTable
	// NewTitleTable is an array of pointers to titles in TitleTable
	NewTitleTable             []uint32
	VideoTable                []VideoTable
	NewVideoTable             []NewVideoTable
	DemoTable                 []DemoTable
	RecommendationTable       []uint32
	RecentRecommendationTable []RecentRecommendationTable
	PopularVideosTable        []PopularVideosTable
	DetailedRatingTable       []DetailedRatingTable

	// Below are variables that help us keep state
	region      constants.Region
	ratingGroup constants.RatingGroup
	language    constants.Language
	// map[game_id]amount_voted
	recommendations map[string]TitleRecommendation
	imageBuffer     *bytes.Buffer
}

// Make text bold
func bold(text string) string {
	return "\033[1m" + text + "\033[0m"
}

func checkError(err error) {
	if err != nil {
		// ERROR! bold and red
		colorFmt.HiRed(bold("An error has occurred!"))
		fmt.Println()
		log.Fatalf(bold("Nintendo Channel file generator has encountered a fatal error!\n\n" + bold("Reason: ") + err.Error() + "\n"))
	}
}

var (
	pool           *pgxpool.Pool
	ctx            = context.Background()
	generateTitles = true
)

// Database credentials (you'll need to change these for your own database)
// Learn how to set up a PostgreSQL database here: https://www.postgresql.org/docs/13/tutorial-start.html
const (
	dbUser     = "user"
	dbPassword = "password"
	dbHost     = "127.0.0.1"
	dbName     = "nintendochannel"
)

func MakeDownloadList(_generateTitles bool) {
	generateTitles = _generateTitles

	// Initialize database
	dbString := fmt.Sprintf("postgres://%s:%s@%s/%s", dbUser, dbPassword, dbHost, dbName)

	dbConf, err := pgxpool.ParseConfig(dbString)
	checkError(err)

	pool, err = pgxpool.ConnectConfig(ctx, dbConf)
	checkError(err)

	defer pool.Close()
	gametdb.PrepareGameTDB()
	info.GetTimePlayed(&ctx, pool)

	wg := sync.WaitGroup{}
	runtime.GOMAXPROCS(runtime.NumCPU())
	semaphore := make(chan struct{}, 3)

	wg.Add(10)
	for _, region := range constants.Regions {
		for _, language := range region.Languages {
			go func(_region constants.RegionMeta, _language constants.Language) {
				defer wg.Done()
				semaphore <- struct{}{}
				fmt.Printf("Starting worker - Region: %d, Language: %d\n", _region.Region, _language)
				list := List{
					region:          _region.Region,
					ratingGroup:     _region.RatingGroup,
					language:        _language,
					imageBuffer:     new(bytes.Buffer),
					recommendations: map[string]TitleRecommendation{},
				}

				list.QueryRecommendations()

				list.MakeHeader()
				list.MakeRatingsTable()
				list.MakeTitleTypeTable()
				list.MakeCompaniesTable()
				list.MakeTitleTable()
				list.MakeNewTitleTable()
				list.MakeVideoTable()
				list.MakeNewVideoTable()
				list.MakeDemoTable()
				list.MakeRecommendationTable()
				list.MakeRecentRecommendationTable()
				list.MakePopularVideoTable()
				list.MakeDetailedRatingTable()
				list.WriteRatingImages()

				temp := bytes.NewBuffer(nil)
				list.WriteAll(temp)
				list.Header.Filesize = uint32(temp.Len())
				temp.Reset()
				list.WriteAll(temp)

				crcTable := crc32.MakeTable(crc32.IEEE)
				checksum := crc32.Checksum(temp.Bytes(), crcTable)
				list.Header.CRC32 = checksum

				temp.Reset()
				list.WriteAll(temp)

				// Compress then write
				compressed, err := lz10.Compress(temp.Bytes())
				checkError(err)

				err = os.WriteFile(fmt.Sprintf("lists/%d/%d/dllist.bin", _region.Region, _language), compressed, 0666)
				checkError(err)
				fmt.Printf("Finished worker - Region: %d, Language: %d\n", _region.Region, _language)
				<-semaphore
			}(region, language)
		}
	}

	wg.Wait()
}

// Write writes the current values in Votes to an io.Writer method.
// This is required as Go cannot write structs with non-fixed slice sizes,
// but can write them individually.
func (l *List) Write(writer io.Writer, data any) {
	err := binary.Write(writer, binary.BigEndian, data)
	checkError(err)
}

func (l *List) WriteAll(writer io.Writer) {
	l.Write(writer, l.Header)
	l.Write(writer, l.RatingsTable)
	l.Write(writer, l.TitleTypesTable)
	l.Write(writer, l.CompaniesTable)
	l.Write(writer, l.TitleTable)
	l.Write(writer, l.NewTitleTable)
	l.Write(writer, l.VideoTable)
	l.Write(writer, l.NewVideoTable)
	l.Write(writer, l.DemoTable)
	l.Write(writer, l.RecommendationTable)
	l.Write(writer, l.RecentRecommendationTable)
	l.Write(writer, l.PopularVideosTable)
	l.Write(writer, l.DetailedRatingTable)
}

// GetCurrentSize returns the current size of our List struct.
// This is useful for calculating the current offset of List.
func (l *List) GetCurrentSize() uint32 {
	buffer := bytes.NewBuffer(nil)
	l.WriteAll(buffer)
	buffer.Write(l.imageBuffer.Bytes())

	return uint32(buffer.Len())
}
