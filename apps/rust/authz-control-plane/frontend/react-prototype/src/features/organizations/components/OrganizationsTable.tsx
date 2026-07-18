// Pure presentation: receives data + selection state, dispatches events.
// No data fetching here — keeps the component reusable in stories/tests.
import { Table, Checkbox, Badge, Flex, Text } from "@radix-ui/themes";
import type { OrganizationRow } from "../api/types";
import type { SelectionState } from "../hooks/useSelection";

interface Props {
  rows: OrganizationRow[];
  selection: SelectionState;
  loading?: boolean;
}

export function OrganizationsTable({ rows, selection, loading }: Props) {
  const allOnPage = rows.map((r) => r.id);
  const allSelected =
    allOnPage.length > 0 && allOnPage.every((id) => selection.isSelected(id));

  return (
    <Table.Root variant="surface">
      <Table.Header>
        <Table.Row>
          <Table.ColumnHeaderCell width="40px">
            <Checkbox
              checked={allSelected}
              onCheckedChange={(c) => selection.toggleMany(allOnPage, !!c)}
              aria-label="select all"
            />
          </Table.ColumnHeaderCell>
          <Table.ColumnHeaderCell>Code</Table.ColumnHeaderCell>
          <Table.ColumnHeaderCell>Name</Table.ColumnHeaderCell>
          <Table.ColumnHeaderCell>Path</Table.ColumnHeaderCell>
          <Table.ColumnHeaderCell align="right">Nodes</Table.ColumnHeaderCell>
          <Table.ColumnHeaderCell align="right">Version</Table.ColumnHeaderCell>
        </Table.Row>
      </Table.Header>
      <Table.Body>
        {loading && rows.length === 0 ? (
          <Table.Row>
            <Table.Cell colSpan={6}>
              <Text color="gray">Loading…</Text>
            </Table.Cell>
          </Table.Row>
        ) : rows.length === 0 ? (
          <Table.Row>
            <Table.Cell colSpan={6}>
              <Text color="gray">No organizations.</Text>
            </Table.Cell>
          </Table.Row>
        ) : (
          rows.map((r) => (
            <Table.Row key={r.id}>
              <Table.Cell>
                <Checkbox
                  checked={selection.isSelected(r.id)}
                  onCheckedChange={() => selection.toggle(r.id)}
                  aria-label={`select ${r.code}`}
                />
              </Table.Cell>
              <Table.RowHeaderCell>
                <Text weight="medium">{r.code}</Text>
              </Table.RowHeaderCell>
              <Table.Cell>{r.name}</Table.Cell>
              <Table.Cell>
                <Flex gap="1">
                  {r.root_path.split(".").map((seg) => (
                    <Badge key={seg} variant="soft">
                      {seg}
                    </Badge>
                  ))}
                </Flex>
              </Table.Cell>
              <Table.Cell align="right">{r.node_count}</Table.Cell>
              <Table.Cell align="right">
                <Badge color="gray">{r.version}</Badge>
              </Table.Cell>
            </Table.Row>
          ))
        )}
      </Table.Body>
    </Table.Root>
  );
}
