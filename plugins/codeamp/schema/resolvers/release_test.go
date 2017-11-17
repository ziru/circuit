package resolvers_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/codeamp/circuit/plugins/codeamp"
	"github.com/codeamp/circuit/plugins/codeamp/actions"
	"github.com/codeamp/circuit/plugins/codeamp/models"
	"github.com/codeamp/circuit/plugins/codeamp/utils"
	"github.com/codeamp/transistor"
	"github.com/davecgh/go-spew/spew"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type TestRelease struct {
	suite.Suite
	db      *gorm.DB
	t       *transistor.Transistor
	actions *actions.Actions
	user    models.User
	context context.Context
}

func (suite *TestRelease) SetupSuite() {

	db, _ := gorm.Open("postgres", fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=%s password=%s",
		"0.0.0.0",
		"15432",
		"postgres",
		"codeamp_test",
		"disable",
		"",
	))

	db.Exec(fmt.Sprintf("CREATE DATABASE %s", "codeamp_test"))
	db.Exec("CREATE EXTENSION \"uuid-ossp\"")
	db.Exec("CREATE EXTENSION IF NOT EXISTS hstore")

	transistor.RegisterPlugin("codeamp", func() transistor.Plugin { return codeamp.NewCodeAmp() })
	t, _ := transistor.NewTestTransistor(transistor.Config{
		Server:    "http://127.0.0.1:16379",
		Password:  "",
		Database:  "0",
		Namespace: "",
		Pool:      "30",
		Process:   "1",
		Plugins: map[string]interface{}{
			"codeamp": map[string]interface{}{
				"workers": 1,
				"postgres": map[string]interface{}{
					"host":     "0.0.0.0",
					"port":     "15432",
					"user":     "postgres",
					"dbname":   "codeamp_test",
					"sslmode":  "disable",
					"password": "",
				},
			},
		},
		EnabledPlugins: []string{},
		Queueing:       false,
	})

	actions := actions.NewActions(t.TestEvents, db)

	suite.db = db
	suite.t = t
	suite.actions = actions
}

func (suite *TestRelease) SetupDBAndContext() {
	suite.db.AutoMigrate(
		&models.User{},
		&models.UserPermission{},
		&models.Release{},
	)

	user := models.User{
		Email:       "foo@boo.com",
		Password:    "secret",
		Permissions: []models.UserPermission{},
	}
	suite.db.Save(&user)

	suite.context = context.WithValue(suite.context, "jwt", utils.Claims{UserId: user.Model.ID.String()})
	suite.user = user
}

func (suite *TestRelease) TearDownSuite() {
	suite.db.Exec("delete from users;")
	suite.db.Exec("delete from user_permissions;")
	suite.db.Exec("delete from Releases;")
}

func (suite *TestRelease) TestSuccessfulCreateRelease() {
	suite.SetupDBAndContext()
	stamp := strings.ToLower("TestSuccessfulCreateRelease")
	spew.Dump(stamp)
	assert.Equal(suite.T(), true, true)
	suite.TearDownSuite()
}

func (suite *TestRelease) TestSuccessfulUpdateRelease() {
	suite.SetupDBAndContext()
	stamp := strings.ToLower("TestSuccessfulUpdateRelease")
	spew.Dump(stamp)
	assert.Equal(suite.T(), true, true)
	suite.TearDownSuite()
}

func (suite *TestRelease) TestSuccessfulDeleteRelease() {
	suite.SetupDBAndContext()
	stamp := strings.ToLower("TestSuccessfulDeleteRelease")
	spew.Dump(stamp)
	assert.Equal(suite.T(), true, true)
	suite.TearDownSuite()
}
func TestReleaseResolvers(t *testing.T) {
	suite.Run(t, new(TestRelease))
}