//! Executable BPMN catalog recognized by the compiler.
//!
//! Keeping this list separate from parsing prevents newly encountered standard
//! elements from disappearing through a wildcard parser branch.

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub(crate) enum BpmnElementDisposition {
    Supported,
    IgnoredMetadata,
    Unsupported,
    Unknown,
}

pub(crate) const UNSUPPORTED_EXECUTABLE_ELEMENTS: &[&str] = &[
    "task",
    "sendTask",
    "receiveTask",
    "manualTask",
    "complexGateway",
    "eventBasedGateway",
    "intermediateCatchEvent",
    "intermediateThrowEvent",
    "transaction",
    "adHocSubProcess",
    "standardLoopCharacteristics",
    "signalEventDefinition",
    "escalationEventDefinition",
    "cancelEventDefinition",
    "conditionalEventDefinition",
    "linkEventDefinition",
    "terminateEventDefinition",
    "choreographyTask",
    "callChoreography",
    "subChoreography",
    "conversation",
    "conversationNode",
    "collaboration",
    "choreography",
    "globalChoreographyTask",
    "participant",
    "messageFlow",
    "conversationLink",
];

pub(crate) const SUPPORTED_ELEMENTS: &[&str] = &[
    "process",
    "startEvent",
    "serviceTask",
    "userTask",
    "scriptTask",
    "businessRuleTask",
    "endEvent",
    "sequenceFlow",
    "exclusiveGateway",
    "inclusiveGateway",
    "parallelGateway",
    "boundaryEvent",
    "compensateEventDefinition",
    "association",
    "multiInstanceLoopCharacteristics",
    "loopCardinality",
    "completionCondition",
    "timerEventDefinition",
    "timeDate",
    "timeDuration",
    "timeCycle",
    "errorEventDefinition",
    "messageEventDefinition",
    "extensionElements",
    "conditionExpression",
    "callActivity",
    "subProcess",
];

pub(crate) fn classify_element(local_name: &str) -> BpmnElementDisposition {
    if is_supported_element(local_name) {
        BpmnElementDisposition::Supported
    } else if is_known_unsupported_executable(local_name) {
        BpmnElementDisposition::Unsupported
    } else if is_ignored_metadata(local_name) {
        BpmnElementDisposition::IgnoredMetadata
    } else {
        BpmnElementDisposition::Unknown
    }
}

fn is_known_unsupported_executable(local_name: &str) -> bool {
    UNSUPPORTED_EXECUTABLE_ELEMENTS.contains(&local_name)
}

fn is_supported_element(local_name: &str) -> bool {
    SUPPORTED_ELEMENTS.contains(&local_name)
}

fn is_ignored_metadata(local_name: &str) -> bool {
    matches!(
        local_name,
        "definitions"
            | "documentation"
            | "import"
            | "incoming"
            | "outgoing"
            | "laneSet"
            | "lane"
            | "flowNodeRef"
            | "message"
            | "error"
            | "signal"
            | "escalation"
            | "itemDefinition"
            | "interface"
            | "operation"
            | "category"
            | "categoryValue"
            | "textAnnotation"
            | "group"
    )
}

#[cfg(test)]
mod tests {
    use super::{
        BpmnElementDisposition, SUPPORTED_ELEMENTS, UNSUPPORTED_EXECUTABLE_ELEMENTS,
        classify_element,
    };

    #[test]
    fn unsupported_catalog_is_also_semantic() {
        for element in UNSUPPORTED_EXECUTABLE_ELEMENTS {
            assert_eq!(
                classify_element(element),
                BpmnElementDisposition::Unsupported
            );
        }
    }

    #[test]
    fn supported_catalog_is_explicit_and_has_no_overlap() {
        for element in SUPPORTED_ELEMENTS {
            assert_eq!(classify_element(element), BpmnElementDisposition::Supported);
            assert!(!UNSUPPORTED_EXECUTABLE_ELEMENTS.contains(element));
        }
    }

    #[test]
    fn diagram_elements_are_not_executable_semantics() {
        // DI lives in another namespace, so these names are never interpreted
        // as BPMN model metadata.
        assert_eq!(
            classify_element("BPMNShape"),
            BpmnElementDisposition::Unknown
        );
        assert_eq!(
            classify_element("definitions"),
            BpmnElementDisposition::IgnoredMetadata
        );
    }
}
