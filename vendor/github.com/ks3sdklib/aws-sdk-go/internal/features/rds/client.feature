# language: en
@rds @client
Feature: Amazon RDS

  Scenario: Making a basic request
    When I call the "DescribeDbEngineVersions" API
    Then the value at "DbEngineVersions" should be a list

  Scenario: Error handling
    When I attempt to call the "DescribeDbInstances" API with:
    | DbInstanceIdentifier | fake-id |
    Then I expect the response error code to be "DBInstanceNotFound"
    And I expect the response error message to include:
    """
    DBInstance fake-id not found.
    """
