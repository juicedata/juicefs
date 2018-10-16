# language: en
@datapipeline @client
Feature: AWS Data Pipeline

  Scenario: Making a basic request
    When I call the "ListPipelines" API
    Then the response should contain a "PipelineIdList"

  Scenario: Error handling
    When I attempt to call the "GetPipelineDefinition" API with:
    | PipelineId | fake-id |
    Then I expect the response error code to be "PipelineNotFoundException"
    And I expect the response error message to include:
    """
    does not exist
    """
