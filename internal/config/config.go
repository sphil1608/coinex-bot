package config

import (
	"github.com/spf13/viper"
)

type Config struct {
	CoinEx    CoinExConfig              `mapstructure:"coinex"`
	Bot       BotConfig                 `mapstructure:"bot"`
	Strategies map[string]StrategyConf  `mapstructure:"strategies"`
	ML        MLConfig                  `mapstructure:"ml"`
	Dashboard DashboardConfig           `mapstructure:"dashboard"`
}

type CoinExConfig struct {
	AccessID      string `mapstructure:"access_id"`
	SecretKey     string `mapstructure:"secret_key"`
	BaseURL       string `mapstructure:"base_url"`
	WSSpotURL     string `mapstructure:"ws_spot_url"`
	WSFuturesURL  string `mapstructure:"ws_futures_url"`
}

type BotConfig struct {
	Mode          string  `mapstructure:"mode"`
	MarketType    string  `mapstructure:"market_type"`
	Market        string  `mapstructure:"market"`
	Strategy      string  `mapstructure:"strategy"`
	BaseQty       string  `mapstructure:"base_qty"`
	MaxOpenOrders int     `mapstructure:"max_open_orders"`
	RiskPerTrade  float64 `mapstructure:"risk_per_trade"`
	StopLossPct   float64 `mapstructure:"stop_loss_pct"`
	TakeProfitPct float64 `mapstructure:"take_profit_pct"`
	Leverage      int     `mapstructure:"leverage"`
	JournalPath   string  `mapstructure:"journal_path"`
}

type StrategyConf map[string]interface{}

type MLConfig struct {
	Enabled          bool    `mapstructure:"enabled"`
	Model            string  `mapstructure:"model"`
	FeatureWindow    int     `mapstructure:"feature_window"`
	RetrainInterval  string  `mapstructure:"retrain_interval"`
	MinConfidence    float64 `mapstructure:"min_confidence"`
}

type DashboardConfig struct {
	Enabled bool `mapstructure:"enabled"`
	Port    int  `mapstructure:"port"`
}

func Load(path string) (*Config, error) {
	viper.SetConfigFile(path)
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
