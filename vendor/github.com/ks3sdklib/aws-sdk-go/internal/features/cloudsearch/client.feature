# language: en
@cloudsearch @client
Feature: Amazon CloudSearch

  Scenario: Making a basic request
    When I call the "DescribeDomains" API
    Then the response should contain a "DomainStatusList"

  Scenario: Error handling
    When I attempt to call the "DescribeIndexFields" API with:
    | DomainName | fakedomain |
    Then I expect the response error code to be "ResourceNotFound"
    And I expect the response error message to include:
    """
    Domain not found: fakedomain
    """
