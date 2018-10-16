# language: en
@cloudtrail @client
Feature: AWS CloudTrail

  Scenario: Making a basic request
    When I call the "DescribeTrails" API
    Then the response should contain a "trailList"

  Scenario: Error handling
    When I attempt to call the "DeleteTrail" API with:
    | Name | faketrail |
    Then I expect the response error code to be "TrailNotFoundException"
    And I expect the response error message to include:
    """
    Unknown trail
    """
