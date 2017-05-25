package bigquery_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	u "github.com/araddon/gou"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"

	"github.com/araddon/qlbridge/datasource"
	"github.com/araddon/qlbridge/plan"
	"github.com/araddon/qlbridge/schema"

	//bqbe "github.com/dataux/dataux/backends/bigquery"
	"github.com/dataux/dataux/frontends/mysqlfe/testmysql"
	"github.com/dataux/dataux/planner"
	tu "github.com/dataux/dataux/testutil"
)

var (
	DbConn              = "root@tcp(127.0.0.1:13307)/datauxtest?parseTime=true"
	loadTestDataOnce    sync.Once
	now                 = time.Now()
	testServicesRunning bool
	btTable             = "datauxtest"
	btInstance          = ""
	gceProject          = os.Getenv("GCEPROJECT")
	_                   = json.RawMessage(nil)
)

func init() {
	if gceProject == "" {
		panic("Must have $GCEPROJECT env")
	}
	tu.Setup()
}

func jobMaker(ctx *plan.Context) (*planner.ExecutorGrid, error) {
	ctx.Schema = testmysql.Schema
	return planner.BuildSqlJob(ctx, testmysql.ServerCtx.PlanGrid)
}

func RunTestServer(t *testing.T) func() {
	if !testServicesRunning {
		testServicesRunning = true
		planner.GridConf.JobMaker = jobMaker
		planner.GridConf.SchemaLoader = testmysql.SchemaLoader
		planner.GridConf.SupressRecover = testmysql.Conf.SupressRecover

		var bqconf *schema.ConfigSource
		for _, sc := range testmysql.Conf.Sources {
			if sc.SourceType == "bigquery" {
				bqconf = sc
			}
		}
		if bqconf == nil {
			panic("must have bigquery conf")
		}
		bqconf.Settings["billing_project"] = gceProject

		testmysql.RunTestServer(t)
	}
	return func() {}
}

func validateQuerySpec(t *testing.T, testSpec tu.QuerySpec) {
	RunTestServer(t)
	tu.ValidateQuerySpec(t, testSpec)
}

func TestShowTables(t *testing.T) {
	// By running testserver, we will load schema/config
	RunTestServer(t)

	data := struct {
		Table string `db:"Table"`
	}{}
	found := false
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "show tables;",
		ExpectRowCt: 8,
		ValidateRowData: func() {
			u.Infof("%+v", data)
			assert.True(t, data.Table != "", "%v", data)
			if data.Table == strings.ToLower("bikeshare_stations") {
				found = true
			}
		},
		RowData: &data,
	})
	assert.True(t, found, "Must have found bikeshare_stations")
}

func TestBasic(t *testing.T) {

	// By running testserver, we will load schema/config
	RunTestServer(t)

	// This is a connection to RunTestServer, which starts on port 13307
	dbx, err := sqlx.Connect("mysql", DbConn)
	assert.True(t, err == nil, "%v", err)
	defer dbx.Close()
	//u.Debugf("%v", testSpec.Sql)
	rows, err := dbx.Queryx(fmt.Sprintf("select * from article"))
	assert.Equal(t, err, nil, "%v", err)
	defer rows.Close()
}

func TestDescribeTable(t *testing.T) {

	data := struct {
		Field   string `db:"Field"`
		Type    string `db:"Type"`
		Null    string `db:"Null"`
		Key     string `db:"Key"`
		Default string `db:"Default"`
		Extra   string `db:"Extra"`
	}{}
	describedCt := 0
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         fmt.Sprintf("describe article;"),
		ExpectRowCt: 10,
		ValidateRowData: func() {
			//u.Infof("%s   %#v", data.Field, data)
			assert.True(t, data.Field != "", "%v", data)
			switch data.Field {
			case "embedded":
				assert.True(t, data.Type == "binary" || data.Type == "text", "%#v", data)
				describedCt++
			case "author":
				assert.True(t, data.Type == "varchar(255)", "data: %#v", data)
				describedCt++
			case "created":
				assert.True(t, data.Type == "datetime", "data: %#v", data)
				describedCt++
			case "category":
				assert.True(t, data.Type == "json", "data: %#v", data)
				describedCt++
			case "body":
				assert.True(t, data.Type == "json", "data: %#v", data)
				describedCt++
			case "deleted":
				assert.True(t, data.Type == "bool" || data.Type == "tinyint", "data: %#v", data)
				describedCt++
			}
		},
		RowData: &data,
	})
	assert.True(t, describedCt == 5, "Should have found/described 5 but was %v", describedCt)
}

func TestSimpleRowSelect(t *testing.T) {

	// bigquery-public-data:san_francisco.bikeshare_stations
	data := struct {
		StationId        int `db:"station_id"`
		Name             string
		Latitude         float64
		InstallationDate time.Time `db:"installation_date"`
	}{}
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select station_id, name, latitude, installation_date from bikeshare_stations WHERE station_id = 6 LIMIT 1",
		ExpectRowCt: 1,
		ValidateRowData: func() {
			u.Infof("%v", data)
			assert.True(t, data.InstallationDate.IsZero() == false, "should have date? %v", data)
			assert.True(t, data.Name == "San Pedro Square", "%v", data)
		},
		RowData: &data,
	})

	return
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select title, count,deleted from bikeshare_stations WHERE count = 22;",
		ExpectRowCt: 1,
		ValidateRowData: func() {
			assert.True(t, data.Name == "article1", "%v", data)
		},
		RowData: &data,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Sql:             "select title, count, deleted from bikeshare_stations LIMIT 10;",
		ExpectRowCt:     4,
		ValidateRowData: func() {},
		RowData:         &data,
	})
}

func TestSelectLimit(t *testing.T) {
	data := struct {
		Title string
		Count int
	}{}
	validateQuerySpec(t, tu.QuerySpec{
		Sql:             "select title, count from article LIMIT 1;",
		ExpectRowCt:     1,
		ValidateRowData: func() {},
		RowData:         &data,
	})
}

func TestSelectGroupBy(t *testing.T) {
	data := struct {
		Author string
		Ct     int
	}{}
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select count(*) as ct, author from article GROUP BY author;",
		ExpectRowCt: 3,
		ValidateRowData: func() {
			//u.Infof("%v", data)
			switch data.Author {
			case "aaron":
				assert.True(t, data.Ct == 1, "Should have found 1? %v", data)
			case "bjorn":
				assert.True(t, data.Ct == 2, "Should have found 2? %v", data)
			}
		},
		RowData: &data,
	})
}

func TestSelectWhereLike(t *testing.T) {

	// We are testing the LIKE clause doesn't exist in Cassandra so we are polyfillying
	data := struct {
		Title  string
		Author string
	}{}
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         `SELECT title, author from article WHERE title like "%stic%"`,
		ExpectRowCt: 1,
		ValidateRowData: func() {
			assert.True(t, data.Title == "listicle1", "%v", data)
		},
		RowData: &data,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         `SELECT title, author from article WHERE title like "list%"`,
		ExpectRowCt: 1,
		ValidateRowData: func() {
			assert.True(t, data.Title == "listicle1", "%v", data)
		},
		RowData: &data,
	})
}

func TestSelectProjectionRewrite(t *testing.T) {

	data := struct {
		Title string
		Ct    int
	}{}
	// We are testing when we need to project twice (1: cassandra, 2: in dataux)
	// - the "count AS ct" alias needs to be rewritten to NOT be projected
	//      in cassandra and or be aware of it since we are projecting again
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         `SELECT title, count AS ct from article WHERE title like "list%"`,
		ExpectRowCt: 1,
		ValidateRowData: func() {
			assert.True(t, data.Title == "listicle1", "%v", data)
		},
		RowData: &data,
	})
}

func TestSelectOrderBy(t *testing.T) {
	RunTestServer(t)

	data := struct {
		Title string
		Ct    int
	}{}
	// Try order by on primary partition key
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select title, count64 AS ct FROM article ORDER BY title DESC LIMIT 1;",
		ExpectRowCt: 1,
		ValidateRowData: func() {
			assert.True(t, data.Title == "zarticle3", "%v", data)
			assert.True(t, data.Ct == 100, "%v", data)
		},
		RowData: &data,
	})

	// try order by on some other keys

	// need to fix OrderBy for ints first
	return
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select title, count64 AS ct FROM article ORDER BY count64 ASC LIMIT 1;",
		ExpectRowCt: 1,
		ValidateRowData: func() {
			assert.True(t, data.Title == "listicle1", "%v", data)
			assert.True(t, data.Ct == 12, "%v", data)
		},
		RowData: &data,
	})
}

func TestMutationInsertSimple(t *testing.T) {
	validateQuerySpec(t, tu.QuerySpec{
		Sql:             "select id, name from user;",
		ExpectRowCt:     3,
		ValidateRowData: func() {},
	})
	validateQuerySpec(t, tu.QuerySpec{
		Exec:            `INSERT INTO user (id, name, deleted, created, updated) VALUES ("user814", "test_name",false, now(), now());`,
		ValidateRowData: func() {},
		ExpectRowCt:     1,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Exec: `
		INSERT INTO user (id, name, deleted, created, updated) 
		VALUES 
			("user815", "test_name2",false, now(), now()),
			("user816", "test_name3",false, now(), now());
		`,
		ValidateRowData: func() {},
		ExpectRowCt:     2,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Sql:             "select id, name from user;",
		ExpectRowCt:     6,
		ValidateRowData: func() {},
	})
}

func TestMutationDeleteSimple(t *testing.T) {
	data := struct {
		Id, Name string
	}{}
	ct := 0
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select id, name from user;",
		ExpectRowCt: -1, // don't evaluate row count
		ValidateRowData: func() {
			ct++
			u.Debugf("data: %+v  ct:%v", data, ct)
		},
		RowData: &data,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Exec: `
			INSERT INTO user (id, name, deleted, created, updated) 
			VALUES 
				("deleteuser123", "test_name",false, now(), now());`,
		ValidateRowData: func() {},
		ExpectRowCt:     1,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Sql:             "select id, name from user;",
		ExpectRowCt:     ct + 1,
		ValidateRowData: func() {},
	})
	validateQuerySpec(t, tu.QuerySpec{
		Exec:            `DELETE FROM user WHERE id = "deleteuser123"`,
		ValidateRowData: func() {},
		ExpectRowCt:     1,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Exec:        `SELECT * FROM user WHERE id = "deleteuser123"`,
		ExpectRowCt: 0,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select id, name from user;",
		ExpectRowCt: ct,
	})
}

func TestMutationUpdateSimple(t *testing.T) {
	data := struct {
		Id      string
		Name    string
		Deleted bool
		Roles   datasource.StringArray
		Created time.Time
		Updated time.Time
	}{}
	validateQuerySpec(t, tu.QuerySpec{
		Exec: `INSERT INTO user 
							(id, name, deleted, created, updated, roles) 
						VALUES 
							("update123", "test_name", false, todate("2014/07/04"), now(), ["admin","sysadmin"]);`,
		ValidateRowData: func() {},
		ExpectRowCt:     1,
	})
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         `select id, name, deleted, roles, created, updated from user WHERE id = "update123"`,
		ExpectRowCt: 1,
		ValidateRowData: func() {
			//u.Infof("%v", data)
			assert.True(t, data.Id == "update123", "%v", data)
			assert.True(t, data.Name == "test_name", "%v", data)
			assert.True(t, data.Deleted == false, "Not deleted? %v", data)
		},
		RowData: &data,
	})
	return
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         `SELECT id, name, deleted, roles, created, updated FROM user WHERE id = "update123"`,
		ExpectRowCt: 1,
		ValidateRowData: func() {
			u.Infof("%v", data)
			assert.True(t, data.Id == "update123", "%v", data)
			assert.True(t, data.Deleted == false, "Not deleted? %v", data)
		},
		RowData: &data,
	})
	//u.Warnf("about to update")
	validateQuerySpec(t, tu.QuerySpec{
		Exec:            `UPDATE user SET name = "was_updated", [deleted] = true WHERE id = "update123"`,
		ValidateRowData: func() {},
		ExpectRowCt:     1,
	})
	//u.Warnf("about to final read")
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         `SELECT id, name, deleted, roles, created, updated FROM user WHERE id = "user815"`,
		ExpectRowCt: 1,
		ValidateRowData: func() {
			u.Infof("%v", data)
			assert.True(t, data.Id == "user815", "fr1 %v", data)
			assert.True(t, data.Name == "was_updated", "fr2 %v", data)
			assert.True(t, data.Deleted == true, "fr3 deleted? %v", data)
		},
		RowData: &data,
	})
}

func TestInvalidQuery(t *testing.T) {
	RunTestServer(t)
	db, err := sql.Open("mysql", DbConn)
	assert.True(t, err == nil)
	// It is parsing the SQL on server side (proxy) not in client
	//  so hence that is what this is testing, making sure proxy responds gracefully
	rows, err := db.Query("select `stuff`, NOTAKEYWORD fake_tablename NOTWHERE `description` LIKE \"database\";")
	assert.True(t, err != nil, "%v", err)
	assert.True(t, rows == nil, "must not get rows")
}
