package proof

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	appchainMgr "github.com/meshplus/bitxhub-core/appchain-mgr"
	"github.com/meshplus/bitxhub-core/validator"
	"github.com/meshplus/bitxhub-kit/log"
	"github.com/meshplus/bitxhub-kit/types"
	"github.com/meshplus/bitxhub-model/constant"
	"github.com/meshplus/bitxhub-model/pb"
	"github.com/meshplus/bitxhub/internal/executor/contracts"
	"github.com/meshplus/bitxhub/internal/ledger"
	"github.com/sirupsen/logrus"
)

type VerifyPool struct {
	proofs sync.Map //ibtp proof cache
	ledger ledger.Ledger
	ve     *validator.ValidationEngine
	logger logrus.FieldLogger
}

func New(ledger ledger.Ledger, logger logrus.FieldLogger) Verify {
	ve := validator.NewValidationEngine(ledger, &sync.Map{}, log.NewWithModule("validator"))
	proofPool := &VerifyPool{
		ledger: ledger,
		logger: logger,
		ve:     ve,
	}
	return proofPool
}

func (pl *VerifyPool) ValidationEngine() *validator.ValidationEngine {
	return pl.ve
}

func (pl *VerifyPool) CheckProof(tx *pb.Transaction) (bool, error) {
	ibtp := tx.IBTP
	if ibtp != nil {
		ok, err := pl.verifyProof(ibtp, tx.Extra)
		if err != nil {
			pl.logger.WithFields(logrus.Fields{
				"hash":  tx.TransactionHash.String(),
				"id":    ibtp.ID(),
				"error": err}).Warn("ibtp verify got error")
			return false, err
		}
		if !ok {
			pl.logger.WithFields(logrus.Fields{
				"hash": tx.TransactionHash.String(),
				"id":   ibtp.ID()}).Warn("ibtp verify failed")
			return false, nil
		}

		//TODO(jz): need to remove the proof
		//tx.Extra = nil
	}
	return true, nil
}

func (pl *VerifyPool) verifyProof(ibtp *pb.IBTP, proof []byte) (bool, error) {
	if proof == nil {
		return false, fmt.Errorf("empty proof")
	}
	proofHash := sha256.Sum256(proof)
	if !bytes.Equal(proofHash[:], ibtp.Proof) {
		return false, fmt.Errorf("proof hash is not correct")
	}

	app := &appchainMgr.Appchain{}
	ok, data := pl.getAccountState(constant.AppchainMgrContractAddr, contracts.AppchainKey(ibtp.From))
	if !ok {
		return false, fmt.Errorf("cannot get registered appchain")
	}
	err := json.Unmarshal(data, app)
	if err != nil {
		return false, fmt.Errorf("unmarshal appchain data fail: %w", err)
	}

	validateAddr := validator.FabricRuleAddr
	rl := &contracts.Rule{}
	ok, data = pl.getAccountState(constant.RuleManagerContractAddr, contracts.RuleKey(ibtp.From))
	if ok {
		if err := json.Unmarshal(data, rl); err != nil {
			return false, fmt.Errorf("unmarshal rule data error: %w", err)
		}
		validateAddr = rl.Address
	} else {
		if app.ChainType != "fabric" {
			return false, fmt.Errorf("appchain didn't register rule")
		}
	}

	ok, err = pl.ve.Validate(validateAddr, ibtp.From, proof, ibtp.Payload, app.Validators)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (pl *VerifyPool) getAccountState(address constant.BoltContractAddress, key string) (bool, []byte) {
	return pl.ledger.GetState(address.Address(), []byte(key))
}

func (pl *VerifyPool) putProof(proofHash types.Hash, proof []byte) {
	pl.proofs.Store(proofHash, proof)
}

func (pl *VerifyPool) GetProof(txHash types.Hash) ([]byte, bool) {
	proof, ok := pl.proofs.Load(txHash)
	if !ok {
		return nil, ok
	}
	return proof.([]byte), ok
}

func (pl *VerifyPool) DeleteProof(txHash types.Hash) {
	pl.proofs.Delete(txHash)
}
