# language: en
@opsworks @client
Feature: AWS OpsWorks

  Scenario: Making a basic request
    When I call the "DescribeStacks" API
    Then the value at "Stacks" should be a list

  Scenario: Error handling
    When I attempt to call the "DescribeLayers" API with:
    | StackId | fake_stack |
    Then I expect the response error code to be "ResourceNotFoundException"
    And I expect the response error message to include:
    """
    Unable to find stack with ID fake_stack
    """
