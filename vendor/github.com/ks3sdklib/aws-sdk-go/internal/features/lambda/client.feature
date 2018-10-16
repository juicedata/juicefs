# language: en
@lambda @client
Feature: Amazon Lambda

  Scenario: Making a basic request
    When I call the "ListEventSourceMappings" API
    Then the value at "EventSourceMappings" should be a list

  Scenario: Error handling
    When I attempt to call the "GetEventSourceMapping" API with:
    | UUID | fake-uuid |
    Then I expect the response error code to be "ResourceNotFoundException"
    And I expect the response error message to include:
    """
    does not exist
    """
