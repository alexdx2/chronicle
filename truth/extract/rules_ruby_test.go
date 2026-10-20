package extract

import (
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestRailsRoutes(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"verbs, resources only, namespace, commented line", `Rails.application.routes.draw do
  get "/health", to: "health#show"
  resources :leads, only: [:index, :show, :create]
  resources :contacts, only: :index
  namespace :api do
    get "/segments", to: "segments#index"
  end
  # resources :deals, only: [:index]
  # get "/old", to: "x#y"
end
`, "GET /api/segments,GET /contacts,GET /health,GET /leads,GET /leads/{},POST /leads"},
		{"all seven actions, except", `Rails.application.routes.draw do
  resources :posts
  resources :tags, except: [:new, :edit, :destroy, :update]
end
`, "DELETE /posts/{},GET /posts,GET /posts/new,GET /posts/{},GET /posts/{}/edit,GET /tags,GET /tags/{},PATCH /posts/{},POST /posts,POST /tags,PUT /posts/{}"},
		{"nested resources, member and collection, scope path", `Rails.application.routes.draw do
  scope "/v2" do
    resources :accounts, only: [:show] do
      resources :invoices, only: %i[index]
      member do
        post :close
      end
      collection do
        get :search
      end
      get :ping
    end
  end
  root "home#index"
end
`, "GET /,GET /v2/accounts/search,GET /v2/accounts/{},GET /v2/accounts/{}/invoices,GET /v2/accounts/{}/ping,POST /v2/accounts/{}/close"},
		{"singular resource, multi-line call, match via, format suffix", `Rails.application.routes.draw do
  resource :profile, only: [:show, :update]
  resources :orders,
    only: [:index]
  match "/hook", to: "hooks#create", via: [:get, :post]
  get "/legacy(.:format)", to: "x#y"
  concern :commentable do
    resources :comments
  end
end
`, "GET /hook,GET /legacy,GET /orders,GET /profile,PATCH /profile,POST /hook,PUT /profile"},
		{"spec file is ignored", "", ""},
	}
	for _, c := range cases {
		files := map[string]string{"config/routes.rb": c.src}
		if c.name == "spec file is ignored" {
			files = map[string]string{"spec/config/routes.rb": "Rails.application.routes.draw do\n get '/x', to: 'a#b'\nend\n"}
		}
		if got := keysOf(t, files, truth.ClassEndpoints); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestRubyKafka(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"ruby-kafka deliver and subscribe", `require "kafka"
kafka.deliver_message(payload, topic: "lead-created")
consumer.subscribe("shipment-created")
consumer.subscribe("a", "b", start_from_beginning: false)
`, "topic:a#consume,topic:b#consume,topic:lead-created#produce,topic:shipment-created#consume"},
		{"waterdrop multi-line", `require "waterdrop"
producer.produce_sync(
  topic: "orders",
  payload: x
)
`, "topic:orders#produce"},
		{"log text and comment are noise", `require "kafka"
logger.info "deliver_message topic: 'ghost'"
# consumer.subscribe("old")
puts "consumer.subscribe('x')"
consumer.subscribe("prefix-#{env}")
consumer.subscribe(topic_name)
`, ""},
		{"no kafka in file", `redis.subscribe("chan")
`, ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"app/svc.rb": c.src}, truth.ClassChannels); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	if got := keysOf(t, map[string]string{"spec/svc_spec.rb": "require 'kafka'\nconsumer.subscribe('x')\n"}, truth.ClassChannels); got != "" {
		t.Errorf("spec: %s", got)
	}
}

func TestRubyHTTP(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"env fetch default with faraday", `conn = Faraday.new(url: ENV.fetch("CMS_SERVICE_URL", "http://cms-svc:8000"))`, "cms-svc"},
		{"env or default with net http", "require 'net/http'\nbase = ENV['PRICING_URL'] || 'https://pricing.internal:9000/v1'\n", "pricing.internal"},
		{"block default", "require 'faraday'\nu = ENV.fetch('X_URL') { 'http://x-svc' }\n", "x-svc"},
		{"direct literal", `HTTParty.get("http://billing-api:3000/ping")`, "billing-api"},
		{"localhost skipped", `Faraday.new(url: ENV.fetch("A_URL", "http://localhost:3000"))`, ""},
		{"non-url default is not a target", `ENV.fetch("KAFKA_BROKERS", "kafka:9092")`, ""},
		{"own address var without client", `APP = ENV.fetch("APP_URL", "https://myapp.example.com")`, ""},
		{"service url var without client", `CAT = ENV.fetch("CATALOG_URL", "http://catalog:3000")`, "catalog"},
		{"commented and in string", "require 'faraday'\n# Faraday.new(url: 'http://old')\nputs \"Faraday.new(url: 'http://txt')\"\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"app/w.rb": c.src}, truth.ClassHTTP); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestGemfileLock(t *testing.T) {
	lock := `GIT
  remote: https://github.com/x/y.git
  revision: abc
  specs:
    ygem (0.1.0)
      rack

GEM
  remote: https://rubygems.org/
  specs:
    rack (3.0.0)
    rails (7.1.3)
      actionpack (= 7.1.3)
    nokogiri (1.16.0-x86_64-linux)
    nokogiri (1.16.0)

PLATFORMS
  ruby

DEPENDENCIES
  rails (~> 7.1)
  ygem!
`
	// only what the Gemfile asks for; the specs closure (rack, nokogiri) is transitive
	if got := keysOf(t, map[string]string{"Gemfile.lock": lock}, truth.ClassDeps); got != "gem:rails,gem:ygem" {
		t.Errorf("got %s", got)
	}
}
