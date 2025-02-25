package mana

import (
	"time"

	"github.com/iotaledger/hive.go/identity"
	"golang.org/x/xerrors"
)

// BaseManaVector is an interface for vectors that store base mana values of nodes in the network.
type BaseManaVector interface {
	// Type returns the type of the base mana vector (access/consensus).
	Type() Type
	// Size returns the size of the base mana vector.
	Size() int
	// Has tells if a certain node is present in the base mana vactor.
	Has(identity.ID) bool
	// Book books mana into the base mana vector.
	Book(*TxInfo)
	// Update updates the mana entries for a particular node wrt time.
	Update(identity.ID, time.Time) error
	// UpdateAll updates all entries in the base mana vector wrt to time.
	UpdateAll(time.Time) error
	// GetMana returns the mana value of a node with default weights.
	GetMana(identity.ID, ...time.Time) (float64, time.Time, error)
	// GetManaMap returns the map derived from the vector.
	GetManaMap(...time.Time) (NodeMap, time.Time, error)
	// GetHighestManaNodes returns the n highest mana nodes in descending order.
	GetHighestManaNodes(uint) ([]Node, time.Time, error)
	// GetHighestManaNodesFraction returns the highest mana that own 'p' percent of total mana.
	GetHighestManaNodesFraction(p float64) ([]Node, time.Time, error)
	// SetMana sets the base mana for a node.
	SetMana(identity.ID, BaseMana)
	// ForEach executes a callback function for each entry in the vector.
	ForEach(func(identity.ID, BaseMana) bool)
	// ToPersistables converts the BaseManaVector to a list of persistable mana objects.
	ToPersistables() []*PersistableBaseMana
	// FromPersistable fills the BaseManaVector from persistable mana objects.
	FromPersistable(*PersistableBaseMana) error
	// RemoveZeroNodes removes all zero mana nodes from the mana vector.
	RemoveZeroNodes()
}

// NewBaseManaVector creates and returns a new base mana vector for the specified type.
func NewBaseManaVector(vectorType Type) (BaseManaVector, error) {
	switch vectorType {
	case AccessMana:
		return &AccessBaseManaVector{
			vector: make(map[identity.ID]*AccessBaseMana),
		}, nil
	case ConsensusMana:
		return &ConsensusBaseManaVector{
			vector: make(map[identity.ID]*ConsensusBaseMana),
		}, nil
	default:
		return nil, xerrors.Errorf("error while creating base mana vector with type %d: %w", vectorType, ErrUnknownManaType)
	}
}

// NewResearchBaseManaVector creates a base mana vector for research purposes.
func NewResearchBaseManaVector(vectorType Type, targetMana Type, weight float64) (BaseManaVector, error) {
	if targetMana != AccessMana && targetMana != ConsensusMana {
		return nil, xerrors.Errorf(
			"targetMana must be either %s or %s, but it is %s: %w",
			AccessMana.String(),
			ConsensusMana.String(),
			targetMana.String(),
			ErrInvalidTargetManaType,
		)
	}
	switch vectorType {
	case WeightedMana:
		vec := &WeightedBaseManaVector{
			vector: make(map[identity.ID]*WeightedBaseMana),
			target: targetMana,
		}
		if err := vec.SetWeight(weight); err != nil {
			return nil, xerrors.Errorf("error while creating base mana vector with weight %f: %w", weight, err)
		}
		return vec, nil
	default:
		return nil, xerrors.Errorf("error while creating base mana vector with type %d: %w", vectorType, ErrUnknownManaType)
	}
}
