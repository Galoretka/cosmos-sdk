package upgrade_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/header"
	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/module"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/cosmos/cosmos-sdk/x/upgrade"
	"github.com/cosmos/cosmos-sdk/x/upgrade/keeper"
	"github.com/cosmos/cosmos-sdk/x/upgrade/types"
)

type TestSuite struct {
	preModule appmodule.HasPreBlocker
	keeper    *keeper.Keeper
	ctx       sdk.Context
	baseApp   *baseapp.BaseApp
	encCfg    moduletestutil.TestEncodingConfig
}

func (s *TestSuite) VerifyDoUpgrade(t *testing.T) {
	t.Helper()
	t.Log("Verify that a panic happens at the upgrade height")
	newCtx := s.ctx.WithHeaderInfo(header.Info{Height: s.ctx.HeaderInfo().Height + 1, Time: time.Now()})

	_, err := s.preModule.PreBlock(newCtx)
	require.ErrorContains(t, err, "UPGRADE \"test\" NEEDED at height: 11: ")

	t.Log("Verify that the upgrade can be successfully applied with a handler")
	s.keeper.SetUpgradeHandler("test", func(ctx context.Context, plan types.Plan, vm module.VersionMap) (module.VersionMap, error) {
		return vm, nil
	})

	_, err = s.preModule.PreBlock(newCtx)
	require.NoError(t, err)

	s.VerifyCleared(t, newCtx)
}

func (s *TestSuite) VerifyDoUpgradeWithCtx(t *testing.T, newCtx sdk.Context, proposalName string) {
	t.Helper()
	t.Log("Verify that a panic happens at the upgrade height")

	_, err := s.preModule.PreBlock(newCtx)
	require.ErrorContains(t, err, "UPGRADE \""+proposalName+"\" NEEDED at height: ")

	t.Log("Verify that the upgrade can be successfully applied with a handler")
	s.keeper.SetUpgradeHandler(proposalName, func(ctx context.Context, plan types.Plan, vm module.VersionMap) (module.VersionMap, error) {
		return vm, nil
	})

	_, err = s.preModule.PreBlock(newCtx)
	require.NoError(t, err)

	s.VerifyCleared(t, newCtx)
}

func (s *TestSuite) VerifyCleared(t *testing.T, newCtx sdk.Context) {
	t.Helper()
	t.Log("Verify that the upgrade plan has been cleared")
	_, err := s.keeper.GetUpgradePlan(newCtx)
	require.ErrorIs(t, err, types.ErrNoUpgradePlanFound)
}

func (s *TestSuite) VerifyNotDone(t *testing.T, newCtx sdk.Context, name string) {
	t.Helper()
	t.Log("Verify that upgrade was not done")
	height, err := s.keeper.GetDoneHeight(newCtx, name)
	require.Zero(t, height)
	require.NoError(t, err)
}

func (s *TestSuite) VerifyDone(t *testing.T, newCtx sdk.Context, name string) {
	t.Helper()
	t.Log("Verify that the upgrade plan has been executed")
	height, err := s.keeper.GetDoneHeight(newCtx, name)
	require.NotZero(t, height)
	require.NoError(t, err)
}

func (s *TestSuite) VerifySet(t *testing.T, skipUpgradeHeights map[int64]bool) {
	t.Helper()
	t.Log("Verify if the skip upgrade has been set")

	for k := range skipUpgradeHeights {
		require.True(t, s.keeper.IsSkipHeight(k))
	}
}

func setupTest(t *testing.T, height int64, skip map[int64]bool) *TestSuite {
	t.Helper()
	s := TestSuite{}
	s.encCfg = moduletestutil.MakeTestEncodingConfig(upgrade.AppModuleBasic{})
	key := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(key)
	testCtx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))

	s.baseApp = baseapp.NewBaseApp(
		"upgrade",
		log.NewNopLogger(),
		testCtx.DB,
		s.encCfg.TxConfig.TxDecoder(),
	)

	s.keeper = keeper.NewKeeper(skip, storeService, s.encCfg.Codec, t.TempDir(), nil, authtypes.NewModuleAddress(govtypes.ModuleName).String())
	s.keeper.SetVersionSetter(s.baseApp)

	s.ctx = testCtx.Ctx.WithHeaderInfo(header.Info{Time: time.Now(), Height: height})

	s.preModule = upgrade.NewAppModule(s.keeper, addresscodec.NewBech32Codec("cosmos"))
	return &s
}

func TestRequireFutureBlock(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})
	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: s.ctx.HeaderInfo().Height - 1})
	require.Error(t, err)
	require.True(t, errors.Is(err, sdkerrors.ErrInvalidRequest), err)
}

func TestDoHeightUpgrade(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})
	t.Log("Verify can schedule an upgrade")
	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: s.ctx.HeaderInfo().Height + 1})
	require.NoError(t, err)

	s.VerifyDoUpgrade(t)
}

func TestCanOverwriteScheduleUpgrade(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})
	t.Log("Can overwrite plan")
	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "bad_test", Height: s.ctx.HeaderInfo().Height + 10})
	require.NoError(t, err)
	err = s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: s.ctx.HeaderInfo().Height + 1})
	require.NoError(t, err)

	s.VerifyDoUpgrade(t)
}

func TestHaltIfTooNew(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})
	t.Log("Verify that we don't panic with registered plan not in database at all")
	var called int
	s.keeper.SetUpgradeHandler("future", func(_ context.Context, _ types.Plan, vm module.VersionMap) (module.VersionMap, error) {
		called++
		return vm, nil
	})

	newCtx := s.ctx.WithHeaderInfo(header.Info{Height: s.ctx.HeaderInfo().Height + 1, Time: time.Now()})
	_, err := s.preModule.PreBlock(newCtx)
	require.NoError(t, err)
	require.Equal(t, 0, called)

	t.Log("Verify we error if we have a registered handler ahead of time")
	err = s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "future", Height: s.ctx.HeaderInfo().Height + 3})
	require.NoError(t, err)
	_, err = s.preModule.PreBlock(newCtx)
	require.EqualError(t, err, "BINARY UPDATED BEFORE TRIGGER! UPGRADE \"future\" - in binary but not executed on chain. Downgrade your binary")
	require.Equal(t, 0, called)

	t.Log("Verify we no longer panic if the plan is on time")

	futCtx := s.ctx.WithHeaderInfo(header.Info{Height: s.ctx.HeaderInfo().Height + 3, Time: time.Now()})
	_, err = s.preModule.PreBlock(futCtx)
	require.NoError(t, err)
	require.Equal(t, 1, called)

	s.VerifyCleared(t, futCtx)
}

func TestCanClear(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})
	t.Log("Verify upgrade is scheduled")
	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: s.ctx.HeaderInfo().Height + 100})
	require.NoError(t, err)

	err = s.keeper.ClearUpgradePlan(s.ctx)
	require.NoError(t, err)

	s.VerifyCleared(t, s.ctx)
}

func TestCantApplySameUpgradeTwice(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})
	height := s.ctx.HeaderInfo().Height + 1
	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: height})
	require.NoError(t, err)
	s.VerifyDoUpgrade(t)
	t.Log("Verify an executed upgrade \"test\" can't be rescheduled")
	err = s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: height})
	require.Error(t, err)
	require.True(t, errors.Is(err, sdkerrors.ErrInvalidRequest), err)
}

func TestNoSpuriousUpgrades(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})
	t.Log("Verify that no upgrade panic is triggered in the BeginBlocker when we haven't scheduled an upgrade")
	_, err := s.preModule.PreBlock(s.ctx)
	require.NoError(t, err)
}

func TestPlanStringer(t *testing.T) {
	require.Equal(t, "name:\"test\" time:<seconds:-62135596800 > height:100 ", (&types.Plan{Name: "test", Height: 100, Info: ""}).String())
	require.Equal(t, `name:"test" time:<seconds:-62135596800 > height:100 `, (&types.Plan{Name: "test", Height: 100, Info: ""}).String())
}

func TestContains(t *testing.T) {
	var skipOne int64 = 11
	s := setupTest(t, 10, map[int64]bool{skipOne: true})

	s.VerifySet(t, map[int64]bool{skipOne: true})
	t.Log("case where array contains the element")
	require.True(t, s.keeper.IsSkipHeight(11))

	t.Log("case where array doesn't contain the element")
	require.False(t, s.keeper.IsSkipHeight(4))
}

func TestSkipUpgradeSkippingAll(t *testing.T) {
	var (
		skipOne int64 = 11
		skipTwo int64 = 20
	)
	s := setupTest(t, 10, map[int64]bool{skipOne: true, skipTwo: true})

	newCtx := s.ctx

	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: skipOne})
	require.NoError(t, err)

	t.Log("Verify if skip upgrade flag clears upgrade plan in both cases")
	s.VerifySet(t, map[int64]bool{skipOne: true, skipTwo: true})

	newCtx = newCtx.WithHeaderInfo(header.Info{Height: skipOne})
	_, err = s.preModule.PreBlock(newCtx)
	require.NoError(t, err)

	t.Log("Verify a second proposal also is being cleared")
	err = s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test2", Height: skipTwo})
	require.NoError(t, err)

	newCtx = newCtx.WithHeaderInfo(header.Info{Height: skipTwo})
	_, err = s.preModule.PreBlock(newCtx)
	require.NoError(t, err)

	// To ensure verification is being done only after both upgrades are cleared
	t.Log("Verify if both proposals are cleared")
	s.VerifyCleared(t, s.ctx)
	s.VerifyNotDone(t, s.ctx, "test")
	s.VerifyNotDone(t, s.ctx, "test2")
}

func TestUpgradeSkippingOne(t *testing.T) {
	var (
		skipOne int64 = 11
		skipTwo int64 = 20
	)
	s := setupTest(t, 10, map[int64]bool{skipOne: true})

	newCtx := s.ctx

	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: skipOne})
	require.NoError(t, err)

	t.Log("Verify if skip upgrade flag clears upgrade plan in one case and does upgrade on another")
	s.VerifySet(t, map[int64]bool{skipOne: true})

	// Setting block height of proposal test
	newCtx = newCtx.WithHeaderInfo(header.Info{Height: skipOne})
	_, err = s.preModule.PreBlock(newCtx)
	require.NoError(t, err)

	t.Log("Verify the second proposal is not skipped")
	err = s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test2", Height: skipTwo})
	require.NoError(t, err)
	// Setting block height of proposal test2
	newCtx = newCtx.WithHeaderInfo(header.Info{Height: skipTwo})
	s.VerifyDoUpgradeWithCtx(t, newCtx, "test2")

	t.Log("Verify first proposal is cleared and second is done")
	s.VerifyNotDone(t, s.ctx, "test")
	s.VerifyDone(t, s.ctx, "test2")
}

func TestUpgradeSkippingOnlyTwo(t *testing.T) {
	var (
		skipOne   int64 = 11
		skipTwo   int64 = 20
		skipThree int64 = 25
	)
	s := setupTest(t, 10, map[int64]bool{skipOne: true, skipTwo: true})

	newCtx := s.ctx

	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: skipOne})
	require.NoError(t, err)

	t.Log("Verify if skip upgrade flag clears upgrade plan in both cases and does third upgrade")
	s.VerifySet(t, map[int64]bool{skipOne: true, skipTwo: true})

	// Setting block height of proposal test
	newCtx = newCtx.WithHeaderInfo(header.Info{Height: skipOne})
	_, err = s.preModule.PreBlock(newCtx)
	require.NoError(t, err)

	// A new proposal with height in skipUpgradeHeights
	err = s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test2", Height: skipTwo})
	require.NoError(t, err)
	// Setting block height of proposal test2
	newCtx = newCtx.WithHeaderInfo(header.Info{Height: skipTwo})
	_, err = s.preModule.PreBlock(newCtx)
	require.NoError(t, err)

	t.Log("Verify a new proposal is not skipped")
	err = s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test3", Height: skipThree})
	require.NoError(t, err)
	newCtx = newCtx.WithHeaderInfo(header.Info{Height: skipThree})
	s.VerifyDoUpgradeWithCtx(t, newCtx, "test3")

	t.Log("Verify two proposals are cleared and third is done")
	s.VerifyNotDone(t, s.ctx, "test")
	s.VerifyNotDone(t, s.ctx, "test2")
	s.VerifyDone(t, s.ctx, "test3")
}

func TestUpgradeWithoutSkip(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})
	newCtx := s.ctx.WithHeaderInfo(header.Info{Height: s.ctx.HeaderInfo().Height + 1, Time: time.Now()})
	err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test", Height: s.ctx.HeaderInfo().Height + 1})
	require.NoError(t, err)
	t.Log("Verify if upgrade happens without skip upgrade")
	_, err = s.preModule.PreBlock(newCtx)
	require.ErrorContains(t, err, "UPGRADE \"test\" NEEDED at height:")

	s.VerifyDoUpgrade(t)
	s.VerifyDone(t, s.ctx, "test")
}

func TestDumpUpgradeInfoToFile(t *testing.T) {
	s := setupTest(t, 10, map[int64]bool{})

	// require no error when the upgrade info file does not exist
	_, err := s.keeper.ReadUpgradeInfoFromDisk()
	require.NoError(t, err)

	planHeight := s.ctx.HeaderInfo().Height + 1
	plan := types.Plan{
		Name:   "test",
		Height: 0, // this should be overwritten by DumpUpgradeInfoToFile
	}
	t.Log("verify if upgrade height is dumped to file")
	err = s.keeper.DumpUpgradeInfoToDisk(planHeight, plan)
	require.Nil(t, err)

	upgradeInfo, err := s.keeper.ReadUpgradeInfoFromDisk()
	require.NoError(t, err)

	t.Log("Verify upgrade height from file matches ")
	require.Equal(t, upgradeInfo.Height, planHeight)
	require.Equal(t, upgradeInfo.Name, plan.Name)

	// clear the test file
	upgradeInfoFilePath, err := s.keeper.GetUpgradeInfoPath()
	require.Nil(t, err)
	err = os.Remove(upgradeInfoFilePath)
	require.Nil(t, err)
}

func TestBinaryVersion(t *testing.T) {
	var skipHeight int64 = 15
	s := setupTest(t, 10, map[int64]bool{skipHeight: true})

	testCases := []struct {
		name        string
		preRun      func() sdk.Context
		expectError bool
	}{
		{
			"test not panic: no scheduled upgrade or applied upgrade is present",
			func() sdk.Context {
				return s.ctx
			},
			false,
		},
		{
			"test not panic: upgrade handler is present for last applied upgrade",
			func() sdk.Context {
				s.keeper.SetUpgradeHandler("test0", func(_ context.Context, _ types.Plan, vm module.VersionMap) (module.VersionMap, error) {
					return vm, nil
				})

				err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test0", Height: s.ctx.HeaderInfo().Height + 2})
				require.NoError(t, err)

				newCtx := s.ctx.WithHeaderInfo(header.Info{Height: 12})
				require.NoError(t, s.keeper.ApplyUpgrade(newCtx, types.Plan{
					Name:   "test0",
					Height: 12,
				}))

				return newCtx
			},
			false,
		},
		{
			"test panic: upgrade needed",
			func() sdk.Context {
				err := s.keeper.ScheduleUpgrade(s.ctx, types.Plan{Name: "test2", Height: 13})
				require.NoError(t, err)

				newCtx := s.ctx.WithHeaderInfo(header.Info{Height: 13})
				return newCtx
			},
			true,
		},
	}

	for _, tc := range testCases {
		ctx := tc.preRun()
		_, err := s.preModule.PreBlock(ctx)
		if tc.expectError {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
}

// TestMissingUpgradeHandler tests the scenario when no upgrade handler is present for last applied upgrade
func TestMissingUpgradeHandler(t *testing.T) {
	// Setup test environment similar to TestDowngradeVerification
	encCfg := moduletestutil.MakeTestEncodingConfig(upgrade.AppModuleBasic{})
	key := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(key)
	testCtx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	ctx := testCtx.Ctx.WithHeaderInfo(header.Info{Time: time.Now(), Height: 10})

	// Use the same temp directory for both keepers to share storage
	tempDir := t.TempDir()
	skip := map[int64]bool{}
	k := keeper.NewKeeper(skip, storeService, encCfg.Codec, tempDir, nil, authtypes.NewModuleAddress(govtypes.ModuleName).String())
	m := upgrade.NewAppModule(k, addresscodec.NewBech32Codec("cosmos"))

	// Submit and execute an upgrade plan with handler
	planName := "test_upgrade"
	err := k.ScheduleUpgrade(ctx, types.Plan{Name: planName, Height: ctx.HeaderInfo().Height + 1})
	require.NoError(t, err)
	ctx = ctx.WithHeaderInfo(header.Info{Height: ctx.HeaderInfo().Height + 1})

	// Set the handler and execute upgrade
	k.SetUpgradeHandler(planName, func(_ context.Context, _ types.Plan, vm module.VersionMap) (module.VersionMap, error) {
		return vm, nil
	})

	// Successful upgrade
	_, err = m.PreBlock(ctx)
	require.NoError(t, err)
	ctx = ctx.WithHeaderInfo(header.Info{Height: ctx.HeaderInfo().Height + 1})

	// Now simulate a restart without the upgrade handler (missing handler scenario)
	// Use the same temp directory and store service to maintain state
	newKeeper := keeper.NewKeeper(skip, storeService, encCfg.Codec, tempDir, nil, authtypes.NewModuleAddress(govtypes.ModuleName).String())
	newModule := upgrade.NewAppModule(newKeeper, addresscodec.NewBech32Codec("cosmos"))

	// Verify that the last applied upgrade exists but no handler is present
	lastAppliedPlan, _, err := newKeeper.GetLastCompletedUpgrade(ctx)
	require.NoError(t, err)
	require.Equal(t, planName, lastAppliedPlan)
	require.False(t, newKeeper.HasHandler(planName))

	// This should trigger an error because no handler exists for the last applied upgrade
	_, err = newModule.PreBlock(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "upgrade handler is missing for test_upgrade upgrade plan")
}

func TestDowngradeVerification(t *testing.T) {
	// could not use setupTest() here, because we have to use the same key
	// for the two keepers.
	encCfg := moduletestutil.MakeTestEncodingConfig(upgrade.AppModuleBasic{})
	key := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(key)
	testCtx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	ctx := testCtx.Ctx.WithHeaderInfo(header.Info{Time: time.Now(), Height: 10})

	skip := map[int64]bool{}
	k := keeper.NewKeeper(skip, storeService, encCfg.Codec, t.TempDir(), nil, authtypes.NewModuleAddress(govtypes.ModuleName).String())
	m := upgrade.NewAppModule(k, addresscodec.NewBech32Codec("cosmos"))

	// submit a plan.
	planName := "downgrade"
	err := k.ScheduleUpgrade(ctx, types.Plan{Name: planName, Height: ctx.HeaderInfo().Height + 1})
	require.NoError(t, err)
	ctx = ctx.WithHeaderInfo(header.Info{Height: ctx.HeaderInfo().Height + 1})

	// set the handler.
	k.SetUpgradeHandler(planName, func(_ context.Context, _ types.Plan, vm module.VersionMap) (module.VersionMap, error) {
		return vm, nil
	})

	// successful upgrade.
	_, err = m.PreBlock(ctx)
	require.NoError(t, err)
	ctx = ctx.WithHeaderInfo(header.Info{Height: ctx.HeaderInfo().Height + 1})

	testCases := map[string]struct {
		preRun      func(*keeper.Keeper, sdk.Context, string)
		expectError bool
	}{
		"valid binary": {
			preRun: func(k *keeper.Keeper, ctx sdk.Context, name string) {
				k.SetUpgradeHandler(planName, func(ctx context.Context, plan types.Plan, vm module.VersionMap) (module.VersionMap, error) {
					return vm, nil
				})
			},
		},
		"downgrade with an active plan": {
			preRun: func(k *keeper.Keeper, ctx sdk.Context, name string) {
				err := k.ScheduleUpgrade(ctx, types.Plan{Name: "another" + planName, Height: ctx.HeaderInfo().Height + 1})
				require.NoError(t, err, name)
			},
			expectError: true,
		},
		"downgrade without any active plan": {
			expectError: true,
		},
	}

	for name, tc := range testCases {
		ctx, _ := ctx.CacheContext()

		// downgrade. now keeper does not have the handler.
		k := keeper.NewKeeper(skip, storeService, encCfg.Codec, t.TempDir(), nil, authtypes.NewModuleAddress(govtypes.ModuleName).String())
		m := upgrade.NewAppModule(k, addresscodec.NewBech32Codec("cosmos"))

		// assertions
		lastAppliedPlan, _, err := k.GetLastCompletedUpgrade(ctx)
		require.NoError(t, err)
		require.Equal(t, planName, lastAppliedPlan)
		require.False(t, k.HasHandler(planName))
		require.False(t, k.DowngradeVerified())
		_, err = k.GetUpgradePlan(ctx)
		require.ErrorIs(t, err, types.ErrNoUpgradePlanFound)

		if tc.preRun != nil {
			tc.preRun(k, ctx, name)
		}

		_, err = m.PreBlock(ctx)
		if tc.expectError {
			require.Error(t, err, name)
		} else {
			require.NoError(t, err, name)
		}
	}
}
