// Page composes table + bulk bar + create dialog. URL drives pagination so
// state survives reloads and tab-shares. No business logic lives here —
// page is pure wiring.
import { useState } from "react";
import { Flex, Heading, Button, Box } from "@radix-ui/themes";
import { useUrlNumber } from "@shared/hooks/useUrlState";
import { useOrganizationsList } from "./hooks/useOrganizations";
import { useSelection } from "./hooks/useSelection";
import { OrganizationsTable } from "./components/OrganizationsTable";
import { BulkActionsBar } from "./components/BulkActionsBar";
import { CreateOrganizationDialog } from "./components/CreateOrganizationDialog";

export function OrganizationsPage() {
  const [offset, setOffset] = useUrlNumber("offset", 0);
  const [limit] = useUrlNumber("limit", 50);
  const selection = useSelection();
  const [createOpen, setCreateOpen] = useState(false);

  const query = useOrganizationsList({ offset, limit });
  const rows = query.data ?? [];

  return (
    <Flex direction="column" gap="3" p="4">
      <Flex justify="between" align="center">
        <Heading>Organizations</Heading>
        <Button onClick={() => setCreateOpen(true)}>New organization</Button>
      </Flex>
      <BulkActionsBar count={selection.count} onClear={selection.clear} />
      <OrganizationsTable
        rows={rows}
        selection={selection}
        loading={query.isLoading}
      />
      <Flex gap="2" justify="end">
        <Button
          variant="soft"
          disabled={offset === 0}
          onClick={() => setOffset(Math.max(0, offset - limit))}
        >
          Prev
        </Button>
        <Button
          variant="soft"
          disabled={rows.length < limit}
          onClick={() => setOffset(offset + limit)}
        >
          Next
        </Button>
      </Flex>
      <Box>
        <CreateOrganizationDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
        />
      </Box>
    </Flex>
  );
}
