# language: en
@swf @client
Feature: Amazon Simple Workflow Service

  Scenario: Making a basic request
    When I call the "ListDomains" API with:
    | RegistrationStatus | REGISTERED |
    Then the value at "DomainInfos" should be a list

  Scenario: Error handling
    When I attempt to call the "DescribeDomain" API with:
    | Name | fake_domain |
    Then I expect the response error code to be "UnknownResourceFault"
    And I expect the response error message to include:
    """
    Unknown domain: fake_domain
    """
