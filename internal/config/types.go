package config

// Question represents a quiz question.
type Question struct {
	Text    string   `mapstructure:"text"`
	Choices []string `mapstructure:"choices"`
	Answer  int      `mapstructure:"answer"` // correct answer index
	Points  int      `mapstructure:"points"`
}

// QuestionSet represents a set of quiz questions.
type QuestionSet struct {
	Title     string     `mapstructure:"title"`
	Questions []Question `mapstructure:"questions"`
	TimeLimit int        `mapstructure:"timeLimit"` // time limit for each question in seconds
}

// Config contains all the question sets and the default set to use.
type Config struct {
	Sets       map[string]QuestionSet `mapstructure:"sets"`
	DefaultSet string                 `mapstructure:"defaultSet"` // default question set to use
}
