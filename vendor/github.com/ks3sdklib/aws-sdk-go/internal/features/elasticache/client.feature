# language: en
@elasticache @client
Feature: ElastiCache

  Scenario: Making a basic request
    When I call the "DescribeEvents" API
    Then the value at "Events" should be a list

  Scenario: Error handling
    When I attempt to call the "DescribeCacheClusters" API with:
    | CacheClusterId | fake_cluster |
    Then I expect the response error code to be "InvalidParameterValue"
    And I expect the response error message to include:
    """
    The parameter CacheClusterIdentifier is not a valid identifier.
    """
