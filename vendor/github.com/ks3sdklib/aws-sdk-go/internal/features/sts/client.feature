# language: en
@sts @client
Feature: AWS STS

  Scenario: Making a basic request
    When I call the "GetSessionToken" API
    Then the response should contain a "Credentials"

  Scenario: Error handling
    When I attempt to call the "GetFederationToken" API with:
    | Name   | temp |
    | Policy |      |
    Then I expect the response error code to be "ValidationError"
    And I expect the response error message to include:
    """
    Value '' at 'policy' failed to satisfy constraint
    """
