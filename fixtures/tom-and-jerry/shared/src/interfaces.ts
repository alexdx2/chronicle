export interface BattleOutcome {
  tomId: string;
  jerryId: string;
  weapon: string;
  result: 'TOM_HITS' | 'JERRY_DODGES' | 'TRAP_TRIGGERED' | 'MUTUAL_DESTRUCTION';
  damage: number;
  timestamp: Date;
}

export interface CharacterStatus {
  id: string;
  name: string;
  health: number;
  speed: number;
}
