package api

// ConfigAPIClient implements the config.APIClient interface needed for configuration updates
type ConfigAPIClient struct {
	client *AyoIndoClient
}

// NewConfigAPIClient creates a new client that implements config.APIClient
func NewConfigAPIClient(ayoClient *AyoIndoClient) *ConfigAPIClient {
	return &ConfigAPIClient{
		client: ayoClient,
	}
}

// GetVideoConfiguration implements the config.APIClient interface
func (c *ConfigAPIClient) GetVideoConfiguration() (map[string]interface{}, error) {
	return c.client.GetVideoConfiguration()
}
