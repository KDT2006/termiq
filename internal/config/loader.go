package config

import (
	"fmt"

	"github.com/spf13/viper"
)

func LoadConfig(configPath string) (*Config, error) {
	viper.SetDefault("defaultSet", "general")
	viper.SetDefault("timeLimit", 30)

	viper.SetConfigName("questions")
	viper.SetConfigType("yaml")

	if configPath != "" {
		viper.AddConfigPath(configPath)
	}
	// default config path
	viper.AddConfigPath(".")
	viper.AddConfigPath("config")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := validateConfig(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

func validateConfig(config *Config) error {
	if len(config.Sets) == 0 {
		return fmt.Errorf("no question sets defined in config")
	}

	if _, exists := config.Sets[config.DefaultSet]; !exists {
		return fmt.Errorf("default question set '%s' does not exist in config", config.DefaultSet)
	}

	for name, set := range config.Sets {
		if err := validateQuestionSet(name, set); err != nil {
			return fmt.Errorf("invalid question set '%s': %w", name, err)
		}
	}

	return nil
}

func validateQuestionSet(name string, set QuestionSet) error {
	if len(set.Questions) == 0 {
		return fmt.Errorf("question set '%s' has no questions", name)
	}

	for i, q := range set.Questions {
		if q.Text == "" {
			return fmt.Errorf("question %d in set '%s' has no text", i+1, name)
		}

		if len(q.Choices) < 4 {
			return fmt.Errorf("question %d in set '%s' must have at least 4 choices", i+1, name)
		}

		if q.Answer >= len(q.Choices) || q.Answer < 0 {
			return fmt.Errorf("question %d in set '%s' has invalid answer index", i+1, name)
		}

		if q.Points == 0 {
			q.Points = 10 // default points if not specified
		}
	}

	if set.TimeLimit == 0 {
		set.TimeLimit = 30 // default time limit if not specified
	}

	return nil
}

// GetDefaultSet retrieves the default question set from the config.
func (c *Config) GetDefaultSet() (*QuestionSet, error) {
	set, exists := c.Sets[c.DefaultSet]
	if !exists {
		return nil, fmt.Errorf("question set '%s' does not exist", c.DefaultSet)
	}
	return &set, nil
}

// GetQuestionSet retrieves a specific question set by name.
func (c *Config) GetQuestionSet(name string) (*QuestionSet, error) {
	set, exists := c.Sets[name]
	if !exists {
		return nil, fmt.Errorf("question set '%s' does not exist", name)
	}
	return &set, nil
}
