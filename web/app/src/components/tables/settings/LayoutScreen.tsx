import { List as ListLayoutIcon, Table2 } from 'lucide-react';
import type { TableViewLayout } from '@/atoms';
import { LayoutOption } from './settingsPrimitives';

export function LayoutScreen({
  viewLayout,
  onViewLayoutChange,
}: {
  viewLayout: TableViewLayout;
  onViewLayoutChange: (layout: TableViewLayout) => void;
}) {
  return (
    <div className="space-y-2">
      <LayoutOption
        icon={Table2}
        label="Table"
        description="Spreadsheet-style rows and editable cells."
        active={viewLayout === 'table'}
        onClick={() => onViewLayoutChange('table')}
      />
      <LayoutOption
        icon={ListLayoutIcon}
        label="List"
        description="Readable rows with property previews."
        active={viewLayout === 'list'}
        onClick={() => onViewLayoutChange('list')}
      />
    </div>
  );
}
