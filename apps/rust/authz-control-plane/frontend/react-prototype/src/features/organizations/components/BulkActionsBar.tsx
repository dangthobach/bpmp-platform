// Skeleton bulk-actions toolbar. Actions are dispatched to the parent so
// concrete mutations stay in the page component.
import { Flex, Button, Text } from "@radix-ui/themes";

interface Props {
  count: number;
  onClear: () => void;
  onBulkDeactivate?: () => void;
  onBulkExport?: () => void;
}

export function BulkActionsBar({
  count,
  onClear,
  onBulkDeactivate,
  onBulkExport,
}: Props) {
  if (count === 0) return null;
  return (
    <Flex
      align="center"
      gap="3"
      px="3"
      py="2"
      style={{
        background: "var(--accent-3)",
        borderRadius: 6,
        border: "1px solid var(--accent-6)",
      }}
    >
      <Text weight="medium">{count} selected</Text>
      <Flex gap="2" ml="auto">
        <Button variant="soft" onClick={onBulkExport} disabled={!onBulkExport}>
          Export
        </Button>
        <Button
          variant="soft"
          color="red"
          onClick={onBulkDeactivate}
          disabled={!onBulkDeactivate}
        >
          Deactivate
        </Button>
        <Button variant="ghost" onClick={onClear}>
          Clear
        </Button>
      </Flex>
    </Flex>
  );
}
