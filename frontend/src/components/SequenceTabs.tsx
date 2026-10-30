import { Settings2 } from 'lucide-react';
import type { Bootstrap } from '../types';
import { currentSequenceID, sequenceChoices } from '../lib/sequences';
import { IconButton, PanelTabs } from './ui';

export function SequenceTabs({
  snapshot,
  disabled,
  onSwitch,
  onManage,
}: {
  snapshot: Bootstrap;
  disabled: boolean;
  onSwitch: (id: string) => void;
  onManage: () => void;
}) {
  return (
    <div className="sequence-tabs">
      <PanelTabs
        id="timeline-sequences"
        label="工程序列"
        value={currentSequenceID(snapshot)}
        activationMode="manual"
        disabled={disabled}
        items={sequenceChoices(snapshot).map((s) => ({ value: s.id, label: s.name }))}
        onChange={onSwitch}
      >
        <IconButton
          id="sequence-manager-trigger"
          label="序列管理"
          disabled={disabled}
          onClick={onManage}
        >
          <Settings2 size={15} />
        </IconButton>
      </PanelTabs>
    </div>
  );
}
