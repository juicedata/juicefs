# language: en
@directconnect @client
Feature: AWS Direct Connect

  Scenario: Making a basic request
    When I call the "DescribeConnections" API
    Then the value at "Connections" should be a list

  Scenario: Error handling
    When I attempt to call the "DescribeConnections" API with:
    | connectionId | fake-connection |
    Then I expect the response error code to be "DirectConnectClientException"
    And I expect the response error message to include:
    """
    Connection ID fake-connection has an invalid format
    """
